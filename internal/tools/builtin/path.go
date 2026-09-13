package builtin

import "strings"

// hasParentRef 判断相对路径是否包含 ".." 层级。
// 按路径分段判定，避免把 "a..b" 这类合法文件名误判为路径穿越
// （与 Python 版 `".." in Path(p).parts` 语义一致）。
func hasParentRef(path string) bool {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == ".." {
			return true
		}
	}
	return false
}
