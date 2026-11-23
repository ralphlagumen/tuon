package store

import (
	"strconv"
	"strings"
	"time"
)

// Verified keys (MESSENGER-MITM M4.1): when you've compared a person's
// keys with Messenger on your phone, tuon keeps their KeyPrint so it can
// tell you when they change.

func verifiedKey(chatID string) string { return "verified:" + chatID }

// Verified returns the keys you verified for chatID and when.
func (s *Store) Verified(chatID string) (print string, at time.Time, ok bool) {
	v := s.Setting(verifiedKey(s.Resolve(chatID)), "")
	ts, print, found := strings.Cut(v, "|")
	if !found {
		return "", time.Time{}, false
	}
	n, _ := strconv.ParseInt(ts, 10, 64)
	return print, time.Unix(n, 0), true
}

// SetVerified records print as chatID's verified keys; "" forgets them.
func (s *Store) SetVerified(chatID, print string, at time.Time) error {
	k := verifiedKey(s.Resolve(chatID))
	if print == "" {
		_, err := s.DB.Exec(`DELETE FROM tuon_settings WHERE k = ?`, k)
		return err
	}
	return s.SetSetting(k, strconv.FormatInt(at.Unix(), 10)+"|"+print)
}
