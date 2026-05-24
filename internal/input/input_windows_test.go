//go:build windows

package input

import (
	"testing"
	"unsafe"
)

func TestInputLayoutMatchesWin32(t *testing.T) {
	if got, want := unsafe.Offsetof(input{}.U), uintptr(8); got != want {
		t.Fatalf("INPUT union offset = %d, want %d", got, want)
	}
	if got, want := unsafe.Sizeof(input{}), uintptr(40); got != want {
		t.Fatalf("INPUT size = %d, want %d", got, want)
	}
}
