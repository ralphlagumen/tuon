// Package keystore holds the encrypted-chat keys (whatsmeow's tables: this
// device's private keys, prekeys, sessions with contacts, group keys) in
// their own database, keys.db, encrypted page by page (the Adiantum VFS of
// ncruces/go-sqlite3). The key comes from tuon's vault, so a copy of the
// file is unreadable without Touch ID / the passphrase.
//
// whatsmeow's schema can't take column-level encryption (fixed key lengths,
// lookups by key value), so the whole file is encrypted instead.
package keystore

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/ncruces/go-sqlite3/driver" // registers "sqlite3"
	_ "github.com/ncruces/go-sqlite3/vfs/adiantum"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)

const fileName = "keys.db"

// Path is where keys.db lives in tuon's folder.
func Path(dir string) string { return filepath.Join(dir, fileName) }

// Exists reports whether keys.db has been created.
func Exists(dir string) bool {
	_, err := os.Stat(Path(dir))
	return err == nil
}

// Open opens (or creates) keys.db with a 32-byte key.
func Open(dir string, key []byte) (*sql.DB, error) {
	if len(key) != 32 {
		return nil, errors.New("keystore key must be 32 bytes")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	hexKey := hex.EncodeToString(key)
	// quoted: an unquoted hex key that starts with a digit is a syntax error
	dsn := "file:" + Path(dir) + "?vfs=adiantum" +
		"&_pragma=hexkey('" + hexKey + "')" +
		"&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=secure_delete(1)"
	existed := Exists(dir)
	// errors from the driver can quote the DSN, key included: never pass
	// them on as they are
	clean := func(err error) error {
		return errors.New(strings.ReplaceAll(err.Error(), hexKey, "<key>"))
	}
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, clean(err)
	}
	// a wrong key shows up on the first read: the header won't decrypt
	if _, err := db.Exec(`SELECT count(*) FROM sqlite_master`); err != nil {
		db.Close()
		if !existed { // don't leave an empty file behind
			_ = os.Remove(Path(dir))
		}
		return nil, fmt.Errorf("keys.db won't open with this key: %w", clean(err))
	}
	_ = os.Chmod(Path(dir), 0o600)
	return db, nil
}

// HasDevice reports whether a database holds an encrypted-chat device.
func HasDevice(db *sql.DB) bool {
	var n int
	if db.QueryRow(`SELECT count(*) FROM whatsmeow_device`).Scan(&n) != nil {
		return false
	}
	return n > 0
}

// Tables lists whatsmeow's tables in a database.
func Tables(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'whatsmeow\_%' ESCAPE '\' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			out = append(out, n)
		}
	}
	return out, rows.Err()
}

// Move copies every whatsmeow table from src (tuon.db, plain) into dst
// (keys.db, which must already have whatsmeow's schema), checks the row
// counts, and only then drops the tables from src. Running it again after
// a crash is safe: rows are copied with INSERT OR REPLACE.
func Move(src, dst *sql.DB) (rows int, err error) {
	tables, err := Tables(src)
	if err != nil || len(tables) == 0 {
		return 0, err
	}
	tx, err := dst.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	counts := map[string]int{}
	for _, t := range tables {
		if t == "whatsmeow_version" {
			continue // dst has its own, from whatsmeow's upgrade
		}
		n, err := copyTable(src, tx, t)
		if err != nil {
			return rows, fmt.Errorf("copy %s: %w", t, err)
		}
		counts[t] = n
		rows += n
	}
	if err := tx.Commit(); err != nil {
		return rows, err
	}
	for t, n := range counts { // every row arrived?
		var got int
		if err := dst.QueryRow(`SELECT count(*) FROM "` + t + `"`).Scan(&got); err != nil || got < n {
			return rows, fmt.Errorf("%s: %d rows copied but %d found (%v); keeping the originals", t, n, got, err)
		}
	}
	// now the originals can go (secure_delete zeroes them on disk)
	if _, err := src.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return rows, err
	}
	for _, t := range tables {
		if _, err := src.Exec(`DROP TABLE IF EXISTS "` + t + `"`); err != nil {
			return rows, fmt.Errorf("drop %s: %w", t, err)
		}
	}
	_, _ = src.Exec(`PRAGMA foreign_keys = ON`)
	_, _ = src.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	_, _ = src.Exec(`VACUUM`)
	return rows, nil
}

func copyTable(src *sql.DB, tx *sql.Tx, table string) (int, error) {
	cols, err := columns(src, table)
	if err != nil {
		return 0, err
	}
	// each value plus its SQLite type: drivers read an empty blob as nil,
	// which would come back as NULL (whatsmeow's schema refuses that)
	sel := make([]string, 0, 2*len(cols))
	for _, c := range cols {
		sel = append(sel, `"`+c+`"`, `typeof("`+c+`")`)
	}
	rows, err := src.Query(`SELECT ` + strings.Join(sel, ", ") + ` FROM "` + table + `"`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	q := `INSERT OR REPLACE INTO "` + table + `" ("` + strings.Join(cols, `","`) + `") VALUES (` +
		strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",") + `)`
	stmt, err := tx.Prepare(q)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	n := 0
	raw := make([]any, 2*len(cols))
	ptrs := make([]any, len(raw))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	vals := make([]any, len(cols))
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		for i := range cols {
			v, typ := raw[2*i], fmt.Sprint(raw[2*i+1])
			// an empty blob comes back as []byte(nil) (a typed nil, so
			// v == nil is false); writing that would store NULL
			if b, ok := v.([]byte); ok && b == nil && typ == "blob" {
				v = []byte{}
			}
			if v == nil && typ == "text" {
				v = ""
			}
			vals[i] = v
		}
		if _, err := stmt.Exec(vals...); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

func columns(db *sql.DB, table string) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info('` + table + `')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if rows.Scan(&c) == nil {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no columns in %s", table)
	}
	return out, rows.Err()
}

// Prepare creates or upgrades whatsmeow's schema in keys.db.
func Prepare(ctx context.Context, db *sql.DB) error {
	return sqlstore.NewWithDB(db, "sqlite3", waLog.Noop).Upgrade(ctx)
}
