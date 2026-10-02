package crypto

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func testConfig() Config {
	return Config{Algorithm: "aes-128-cbc", Key: &Material{Value: "0123456789abcdef"}, IV: &Material{Value: "0123456789abcdef"}}
}

// Compatibility vectors from Live-yum/slumber commit 895fd49d,
// crates/core/src/crypto/engine.rs::independent_dotnet_vectors (MIT).
func TestForkDotNetKnownAnswers(t *testing.T) {
	c := testConfig()
	for _, v := range [][2]string{{"", "7Uf+4FRcP6fdBw1EuG6Y2Q=="}, {"13800138000", "UEI1Z8QQS25WGasdhDJA4g=="}, {"测试人员", "zAboTQoxNn96i0Z/KPwKGQ=="}, {"1234567890abcdef", "dbPloHkdKgBH2huMBKZlMkR/vZI3jKgPWz10cus9K10="}} {
		out, e := c.Encode([]byte(v[0]))
		if e != nil || string(out) != v[1] {
			t.Fatalf("vector failed %v", e)
		}
		out, e = c.Decode([]byte(v[1]))
		if e != nil || string(out) != v[0] {
			t.Fatalf("decode failed %v", e)
		}
	}
}
func TestAllAlgorithmsRoundTrip(t *testing.T) {
	for _, bits := range []int{128, 192, 256} {
		for _, mode := range []string{"cbc", "ecb"} {
			for _, transport := range []string{"base64", "base64url"} {
				c := Config{Algorithm: fmt.Sprintf("aes-%d-%s", bits, mode), Key: &Material{Value: strings.Repeat("k", bits/8)}, CiphertextEncoding: transport}
				if mode == "cbc" {
					c.IV = &Material{Value: strings.Repeat("i", 16)}
				}
				for _, p := range [][]byte{nil, []byte("中文\x00test"), bytes.Repeat([]byte{255}, 33)} {
					enc, e := c.Encode(p)
					if e != nil {
						t.Fatal(e)
					}
					dec, e := c.Decode(enc)
					if e != nil || !bytes.Equal(p, dec) {
						t.Fatal("round trip", e)
					}
				}
			}
		}
	}
}
func TestMaterialsAndStrictDecode(t *testing.T) {
	c := testConfig()
	for _, m := range []Material{{Value: "30313233 343536373839616263646566", Encoding: "hex"}, {Value: "MDEyMzQ1Njc4OWFiY2RlZg==", Encoding: "base64"}} {
		c.Key = &m
		v, e := c.Encode([]byte("13800138000"))
		if e != nil || string(v) != "UEI1Z8QQS25WGasdhDJA4g==" {
			t.Fatal(e)
		}
	}
	for _, s := range []string{"!", "YQ==", "", "UEI1Z8QQS25WGasdhDJA4g=", "UEI1Z8QQS25WGasdhDJA4g==\n"} {
		if _, e := c.Decode([]byte(s)); e == nil {
			t.Fatal("accepted bad", s)
		}
	}
	c.Key = &Material{Value: "fedcba9876543210"}
	if _, e := c.Decode([]byte("UEI1Z8QQS25WGasdhDJA4g==")); e == nil {
		t.Fatal("wrong vector key accepted")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", c, c), "fedcba") {
		t.Fatal("key logged")
	}
	b := Config{Algorithm: "base64url", Base64Decode: DecodeOptions{true, true}}
	v, e := b.Decode([]byte(" /w \n"))
	if e != nil || !bytes.Equal(v, []byte{255}) {
		t.Fatal(e)
	}
	b.Base64Decode = DecodeOptions{}
	if _, e = b.Decode([]byte("YR==")); e == nil {
		t.Fatal("noncanonical trailing bits")
	}
}
func TestTransactionalTransforms(t *testing.T) {
	codecs := map[string]Config{"am": testConfig(), "b64": {Algorithm: "base64"}}
	raw := []byte(`{"list":[{"phone":"UEI1Z8QQS25WGasdhDJA4g=="},{"phone":null},{}],"huge":1844674407370955161600001}`)
	r := Rule{Type: "decrypt", Crypto: "am", Paths: []string{"$.list[*].phone", "$.list[0].phone"}, SkipMissing: true, SkipNull: true}
	out, e := Transform(raw, codecs, []Rule{r})
	if e != nil || !bytes.Contains(out, []byte("13800138000")) || !bytes.Contains(out, []byte("1844674407370955161600001")) {
		t.Fatal(string(out), e)
	}
	bad := bytes.Replace(raw, []byte("null"), []byte(`"PRIVATE-BAD"`), 1)
	out, e = Transform(bad, codecs, []Rule{r})
	if e == nil || out != nil || strings.Contains(e.Error(), "PRIVATE") {
		t.Fatal("partial/leaking failure")
	}
	body := base64.StdEncoding.EncodeToString(append([]byte{239, 187, 191}, raw...))
	out, e = Transform([]byte(body), codecs, []Rule{{Type: "decode", Crypto: "b64", Scope: "body", Parse: "json", TextEncoding: "utf8-sig"}, r})
	if e != nil || !bytes.Contains(out, []byte("13800138000")) {
		t.Fatal(e)
	}
	out, e = Transform([]byte(`{"data":"{\"x\":\"aGk=\"}"}`), codecs, []Rule{{Type: "parse_json", Paths: []string{"$.data"}}, {Type: "decode", Crypto: "b64", Paths: []string{"$.data[\"x\"]"}}})
	if e != nil || !bytes.Contains(out, []byte("hi")) {
		t.Fatal(e)
	}
	for _, p := range []string{"$.missing", "$..x", "$[?(@.x)]", "$.list[9].phone"} {
		if _, e := Transform(raw, codecs, []Rule{{Type: "decrypt", Crypto: "am", Paths: []string{p}}}); e == nil {
			t.Fatal("bad path accepted", p)
		}
	}
}

func TestInvalidConfigAndConflict(t *testing.T) {
	for _, c := range []Config{
		{Algorithm: "aes-128-cbc", Key: &Material{Value: "short"}, IV: &Material{Value: "0123456789abcdef"}},
		{Algorithm: "aes-128-cbc", Key: &Material{Value: "0123456789abcdef"}, IV: &Material{Value: "short"}},
		{Algorithm: "aes-128-ecb", Key: &Material{Value: "0123456789abcdef"}, IV: &Material{Value: "0123456789abcdef"}},
		{Algorithm: "base64", Key: &Material{Value: "secret"}},
		{Algorithm: "aes-128-cbc", Key: &Material{Value: "0123456789abcdef"}, IV: &Material{Value: "0123456789abcdef"}, Padding: "none"},
	} {
		if _, e := c.Encode([]byte("test")); e == nil {
			t.Fatal("invalid config accepted")
		}
	}
	c := testConfig()
	enc, _ := c.Encode([]byte("1234567890abcdef"))
	b, _ := base64.StdEncoding.DecodeString(string(enc))
	b[15] ^= 16 // corrupt final plaintext padding deterministically via preceding CBC block
	if _, e := c.Decode([]byte(base64.StdEncoding.EncodeToString(b))); e == nil {
		t.Fatal("zero padding accepted")
	}
	_, e := Transform([]byte(`{"v":"aGk="}`), map[string]Config{"b": {Algorithm: "base64"}}, []Rule{{Type: "decode", Crypto: "b", Paths: []string{"$.v"}}, {Type: "decode", Crypto: "b", Paths: []string{"$.v"}, SkipNull: true}})
	if e == nil {
		t.Fatal("conflicting rules accepted")
	}
}
