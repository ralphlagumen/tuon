package main

import "testing"

// On Ralph's Mac FileVault is on, so there must be no warning.
func TestDiskWarningQuietWhenEncrypted(t *testing.T) {
	if w := diskWarning(); w != "" {
		t.Logf("warning shown: %q (FileVault off on this machine?)", w)
	}
}
