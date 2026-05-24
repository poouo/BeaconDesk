package updatecheck

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name   string
		a      string
		b      string
		want   int
		wantOK bool
	}{
		{name: "same", a: "v1.2.3", b: "1.2.3", want: 0, wantOK: true},
		{name: "older", a: "v1.2.3", b: "v1.3.0", want: -1, wantOK: true},
		{name: "newer", a: "2.0.0", b: "1.9.9", want: 1, wantOK: true},
		{name: "missing patch", a: "1.2", b: "1.2.1", want: -1, wantOK: true},
		{name: "prerelease", a: "1.2.3-beta.1", b: "1.2.3", want: 0, wantOK: true},
		{name: "dev", a: "dev", b: "1.0.0", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CompareVersions(tt.a, tt.b)
			if ok != tt.wantOK {
				t.Fatalf("ok = %t, want %t", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Fatalf("compare = %d, want %d", got, tt.want)
			}
		})
	}
}
