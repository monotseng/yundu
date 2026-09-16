package aiassist

import (
	"net"
	"testing"
)

func TestValidateRejectsUnsafeOrOversizedConfig(t *testing.T) {
	cases := []Config{{BaseURL: "http://model.example", Model: "m", MonthlyTokenBudget: 1}, {BaseURL: "https://127.0.0.1", Model: "m", MonthlyTokenBudget: 1}, {BaseURL: "https://model.example?token=secret", Model: "m", MonthlyTokenBudget: 1}, {BaseURL: "https://model.example", Model: "m", MonthlyTokenBudget: 1, DailyPerUser: 21}, {BaseURL: "https://model.example", Model: "m", MonthlyTokenBudget: 1, MaxInputChars: 4001}}
	for i := range cases {
		if validate(&cases[i]) == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}
func TestValidateAppliesBoundedDefaults(t *testing.T) {
	c := Config{BaseURL: "https://model.example/v1", Model: "approved-model", MonthlyTokenBudget: 10000}
	if err := validate(&c); err != nil {
		t.Fatal(err)
	}
	if c.DailyPerUser != 20 || c.TimeoutSeconds != 30 || c.MaxInputChars != 4000 || c.MaxOutputTokens != 1000 {
		t.Fatalf("unexpected defaults: %#v", c)
	}
}
func TestForbiddenDestinationAddresses(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "fc00::1", "224.0.0.1"} {
		if !forbiddenIP(net.ParseIP(raw)) {
			t.Errorf("%s should be forbidden", raw)
		}
	}
	if forbiddenIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public address rejected")
	}
}
