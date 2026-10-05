// Package secret 是"不会被意外打印"的秘密类型（设计稿 §10.5）。
//
// 任何格式化（%s / %v / %+v / %#v、String、JSON）都只输出掩码 `adsk_dev_ab12…`；需要明文的地方必须显式调
// Reveal —— 写凭证文件、设置 agent 的环境变量、HTTP Authorization 头，仅此三处。
package secret

import (
	"encoding/json"
	"fmt"
)

// Secret 是一个秘密值。
type Secret struct {
	value string
}

// New 包装明文。
func New(value string) Secret { return Secret{value: value} }

// Reveal 返回明文（调用点即"秘密离开内存的地方"，评审时逐个看）。
func (s Secret) Reveal() string { return s.value }

// Empty 报告是否为空。
func (s Secret) Empty() bool { return s.value == "" }

// String 返回掩码。
func (s Secret) String() string { return Mask(s.value) }

// GoString 返回掩码（%#v）。
func (s Secret) GoString() string { return "secret.Secret(" + Mask(s.value) + ")" }

// Format 让所有动词都输出掩码。
func (s Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(Mask(s.value))) }

// MarshalJSON 输出掩码：秘密不该经由通用 JSON 序列化落盘（凭证文件用显式的 DTO 写明文）。
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(Mask(s.value)) }

// Mask 保留前缀（类型 + 前几位）后接省略号；短值整段隐藏。
func Mask(value string) string {
	const keep = 13 // "adsk_dev_" + 4 位
	if value == "" {
		return ""
	}
	if len(value) <= keep+4 {
		return "****"
	}
	return value[:keep] + "…"
}
