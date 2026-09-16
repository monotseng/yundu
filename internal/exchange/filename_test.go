package exchange

import "testing"

func TestValidateFilename(t *testing.T) {
	for _, name := range []string{"数据 导出.csv", "脚本.sql"} {
		if err := ValidateFilename(name); err != nil {
			t.Errorf("rejected %q: %v", name, err)
		}
	}
	for _, name := range []string{"../x.csv", "dir/x.csv", "CON.csv", "x.csv.", "a\u202Ecsv.sql", "x\x00.csv"} {
		if err := ValidateFilename(name); err == nil {
			t.Errorf("accepted unsafe %q", name)
		}
	}
}
