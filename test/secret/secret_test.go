package secret_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/developstack/aidevstack/cli/internal/secret"
)

// TestSecretNeverFormatsPlaintext 任何格式化与 JSON 都只输出掩码；只有 Reveal 给明文。
func TestSecretNeverFormatsPlaintext(t *testing.T) {
	const plain = "adsk_dev_0123456789abcdef0123456789abcdef"
	s := secret.New(plain)
	type holder struct{ Token secret.Secret }
	outputs := []string{
		s.String(), fmt.Sprint(s), fmt.Sprintf("%s %v %+v %#v %q", s, s, s, s, s),
		fmt.Sprintf("%+v", holder{s}), fmt.Sprintf("%#v", holder{s}),
	}
	raw, err := json.Marshal(holder{s})
	if err != nil {
		t.Fatal(err)
	}
	outputs = append(outputs, string(raw))
	for _, out := range outputs {
		if strings.Contains(out, "0123456789abcdef") {
			t.Fatalf("明文泄露：%s", out)
		}
	}
	if s.String() != "adsk_dev_0123…" || s.Reveal() != plain {
		t.Fatalf("mask = %q", s.String())
	}
	if secret.Mask("short") != "****" || secret.Mask("") != "" || !secret.New("").Empty() {
		t.Fatal("短值 / 空值掩码")
	}
}
