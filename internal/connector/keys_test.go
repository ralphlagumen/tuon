package connector

import (
	"bytes"
	"testing"
)

func TestKeyChanges(t *testing.T) {
	k := func(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }
	was := KeyPrint([]DeviceKey{{Device: 10, Key: k(1)}, {Device: 2, Key: k(2)}, {Device: 4}})
	if c, a := KeyChanges(was, []DeviceKey{{Device: 2, Key: k(2)}, {Device: 10, Key: k(1)}}); len(c)+len(a) != 0 {
		t.Fatalf("same keys, any order: %v %v", c, a)
	}
	c, a := KeyChanges(was, []DeviceKey{{Device: 2, Key: k(9)}, {Device: 5, Key: k(5)}, {Device: 6}})
	if len(c) != 1 || c[0] != 2 || len(a) != 1 || a[0] != 5 {
		t.Fatalf("changed %v added %v", c, a)
	}
	if c, a := KeyChanges(was, []DeviceKey{{Device: 2, Key: k(2)}}); len(c)+len(a) != 0 {
		t.Fatal("a device that went away isn't a change")
	}
}
