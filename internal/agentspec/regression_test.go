package agentspec

import "testing"

// Agent 名称会直接作为 .aidog/agents/<name>.yml 的文件名，
// 非法字符与 Windows 保留名必须在保存前给出可读错误。
func TestValidateNameRejectsWindowsInvalid(t *testing.T) {
	bad := []string{
		"a:b", "a*b", "a?b", `a"b`, "a<b", "a>b", "a|b",
		"CON", "con.txt", "LPT1", "nul.json", "aux",
		"a\tb", // 控制字符
	}
	for _, name := range bad {
		if err := validateName(name); err == nil {
			t.Fatalf("名称 %q 应被拒绝", name)
		}
	}

	good := []string{"reviewer", "go-review", "子 Agent", "agent.v2", "a_b"}
	for _, name := range good {
		if err := validateName(name); err != nil {
			t.Fatalf("名称 %q 应被接受: %v", name, err)
		}
	}
}
