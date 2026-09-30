// Package crypto implements the wire codecs of Live-yum/slumber native crypto.
// It uses only Go's standard library and never logs or persists key material.
package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Material struct {
	Value    string `json:"value"`
	Encoding string `json:"encoding,omitempty"`
}

func (m Material) String() string   { return "[密钥材料已隐藏]" }
func (m Material) GoString() string { return m.String() }

type DecodeOptions struct {
	IgnoreWhitespace    bool `json:"ignore_ascii_whitespace,omitempty"`
	AllowMissingPadding bool `json:"allow_missing_padding,omitempty"`
}
type Config struct {
	Algorithm          string        `json:"algorithm"`
	Key                *Material     `json:"key,omitempty"`
	IV                 *Material     `json:"iv,omitempty"`
	Padding            string        `json:"padding,omitempty"`
	PlaintextEncoding  string        `json:"plaintext_encoding,omitempty"`
	CiphertextEncoding string        `json:"ciphertext_encoding,omitempty"`
	Base64Decode       DecodeOptions `json:"base64_decode,omitempty"`
}

func Parse(v any, out any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return errors.New("加解密配置无效")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(out); e != nil {
		return errors.New("加解密配置含未知字段或类型错误（材料已隐藏）")
	}
	return nil
}
func whitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == 11 || b == 12
}
func decode64(input []byte, url bool, opt DecodeOptions) ([]byte, error) {
	b := make([]byte, 0, len(input)+3)
	for _, c := range input {
		if whitespace(c) {
			if opt.IgnoreWhitespace {
				continue
			}
			return nil, errors.New("Base64 含未允许的空白")
		}
		if url {
			if c == '-' {
				c = '+'
			}
			if c == '_' {
				c = '/'
			}
		}
		b = append(b, c)
	}
	if opt.AllowMissingPadding {
		for len(b)%4 != 0 {
			b = append(b, '=')
		}
	}
	out, e := base64.StdEncoding.Strict().DecodeString(string(b))
	if e != nil {
		return nil, errors.New("Base64 字母、长度或填充无效")
	}
	return out, nil
}
func material(m *Material, opt DecodeOptions) ([]byte, error) {
	if m == nil {
		return nil, errors.New("缺少 key/iv 材料")
	}
	switch m.Encoding {
	case "", "utf8", "text":
		return []byte(m.Value), nil
	case "hex":
		b := strings.Map(func(r rune) rune {
			if r < 128 && whitespace(byte(r)) {
				return -1
			}
			return r
		}, m.Value)
		v, e := hex.DecodeString(b)
		if e != nil {
			return nil, errors.New("key/iv 十六进制无效（材料已隐藏）")
		}
		return v, nil
	case "base64":
		v, e := decode64([]byte(m.Value), false, opt)
		if e != nil {
			return nil, errors.New("key/iv Base64 无效（材料已隐藏）")
		}
		return v, nil
	}
	return nil, errors.New("不支持的 key/iv 编码")
}
func (c Config) IsAES() bool { return strings.HasPrefix(c.Algorithm, "aes-") }
func (c Config) Validate() error {
	size := 0
	switch c.Algorithm {
	case "none", "base64", "base64url":
		if c.Key != nil || c.IV != nil || c.Padding != "" || c.PlaintextEncoding != "" || c.CiphertextEncoding != "" {
			return errors.New("非 AES 算法不能设置 AES 材料或选项")
		}
		if c.Algorithm == "none" && c.Base64Decode != (DecodeOptions{}) {
			return errors.New("none 不使用 Base64 选项")
		}
		return nil
	case "aes-128-cbc", "aes-128-ecb":
		size = 16
	case "aes-192-cbc", "aes-192-ecb":
		size = 24
	case "aes-256-cbc", "aes-256-ecb":
		size = 32
	default:
		return errors.New("不支持的加解密算法")
	}
	if c.Padding != "" && c.Padding != "pkcs7" {
		return errors.New("AES 仅支持 pkcs7")
	}
	if c.PlaintextEncoding != "" && c.PlaintextEncoding != "utf8" {
		return errors.New("明文编码仅支持 utf8")
	}
	if c.CiphertextEncoding != "" && c.CiphertextEncoding != "base64" && c.CiphertextEncoding != "base64url" {
		return errors.New("密文编码仅支持 base64/base64url")
	}
	k, e := material(c.Key, c.Base64Decode)
	if e != nil {
		return e
	}
	defer clear(k)
	if len(k) != size {
		return fmt.Errorf("AES 密钥必须为 %d 字节", size)
	}
	if strings.HasSuffix(c.Algorithm, "cbc") {
		iv, e := material(c.IV, c.Base64Decode)
		if e != nil {
			return e
		}
		defer clear(iv)
		if len(iv) != 16 {
			return errors.New("CBC IV 必须为 16 字节")
		}
	} else if c.IV != nil {
		return errors.New("ECB 不能设置 IV")
	}
	return nil
}
func (c Config) Encode(in []byte) ([]byte, error) { return c.convert(in, false) }
func (c Config) Decode(in []byte) ([]byte, error) { return c.convert(in, true) }
func (c Config) convert(in []byte, decode bool) ([]byte, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if c.Algorithm == "none" {
		return bytes.Clone(in), nil
	}
	url := c.Algorithm == "base64url" || c.CiphertextEncoding == "base64url"
	enc := base64.StdEncoding
	if url {
		enc = base64.URLEncoding
	}
	if !c.IsAES() {
		if decode {
			return decode64(in, url, c.Base64Decode)
		}
		return []byte(enc.EncodeToString(in)), nil
	}
	key, _ := material(c.Key, c.Base64Decode)
	defer clear(key)
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, errors.New("AES 材料无效")
	}
	var iv []byte
	if c.IV != nil {
		iv, _ = material(c.IV, c.Base64Decode)
		defer clear(iv)
	}
	var data []byte
	if decode {
		data, e = decode64(in, url, c.Base64Decode)
		if e != nil {
			return nil, e
		}
		if len(data) == 0 || len(data)%16 != 0 {
			return nil, errors.New("AES 密文必须为正整数个 16 字节块")
		}
	} else {
		pad := 16 - len(in)%16
		data = append(bytes.Clone(in), bytes.Repeat([]byte{byte(pad)}, pad)...)
	}
	if strings.HasSuffix(c.Algorithm, "cbc") {
		if decode {
			cipher.NewCBCDecrypter(block, iv).CryptBlocks(data, data)
		} else {
			cipher.NewCBCEncrypter(block, iv).CryptBlocks(data, data)
		}
	} else {
		for i := 0; i < len(data); i += 16 {
			if decode {
				block.Decrypt(data[i:i+16], data[i:i+16])
			} else {
				block.Encrypt(data[i:i+16], data[i:i+16])
			}
		}
	}
	if !decode {
		return []byte(enc.EncodeToString(data)), nil
	}
	pad := int(data[len(data)-1])
	valid := subtle.ConstantTimeLessOrEq(1, pad) & subtle.ConstantTimeLessOrEq(pad, 16)
	for i := 0; i < 16; i++ {
		check := subtle.ConstantTimeLessOrEq(i+1, pad)
		valid &= subtle.ConstantTimeSelect(check, subtle.ConstantTimeByteEq(data[len(data)-1-i], byte(pad)), 1)
	}
	if valid != 1 {
		clear(data)
		return nil, errors.New("PKCS7 填充无效；CBC/ECB 不提供认证")
	}
	return data[:len(data)-pad], nil
}
