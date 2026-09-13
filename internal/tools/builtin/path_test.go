package builtin

import "testing"

// hasParentRef 按路径分段判定 ".."，不应把 "a..b" 之类的合法文件名误判为穿越。
func TestHasParentRef(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"..", true},
		{"../a", true},
		{"a/../b", true},
		{"a..b", false},
		{"..a", false},
		{"a/b", false},
		{"..\\a", true},
	}

	for _, tc := range cases {
		if got := hasParentRef(tc.path); got != tc.want {
			t.Errorf("hasParentRef(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
