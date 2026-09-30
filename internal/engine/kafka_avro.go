package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/linkedin/goavro/v2"
)

// Set decoder safety limits once, before any concurrent operations. goavro's
// library defaults allow enormous allocations from untrusted block counts.
func init() {
	goavro.MaxBlockCount = 65536
	goavro.MaxBlockSize = maxBody
}

// A registry is scoped to one operation: no credential-bearing global cache.
type kafkaRegistry struct {
	base                       string
	client                     *http.Client
	transport                  *http.Transport
	username, password, bearer string
	codecs                     map[uint32]*goavro.Codec
}

func newKafkaRegistry(r config.Request) (*kafkaRegistry, error) {
	raw := r.String("schema_registry_url", "")
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("schema_registry_url must be an absolute http(s) URL without credentials, query or fragment")
	}
	p := map[string]any{}
	for _, k := range []string{"ca_file", "cert_file", "key_file"} {
		if v, ok := r.Params["schema_registry_"+k]; ok {
			p[k] = v
		}
	}
	tls, err := tlsConfig(config.Request{Params: p})
	if err != nil {
		return nil, fmt.Errorf("schema registry TLS configuration invalid")
	}
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: tls}
	return &kafkaRegistry{base: strings.TrimRight(raw, "/"), transport: tr, client: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, username: r.String("schema_registry_username", ""), password: r.String("schema_registry_password", ""), bearer: r.String("schema_registry_bearer", ""), codecs: map[uint32]*goavro.Codec{}}, nil
}
func (s *kafkaRegistry) close() {
	if s != nil {
		s.transport.CloseIdleConnections()
	}
}

type registrySchema struct {
	ID         uint32            `json:"id"`
	Schema     string            `json:"schema"`
	Type       string            `json:"schemaType"`
	References []json.RawMessage `json:"references"`
}

func (s *kafkaRegistry) get(ctx context.Context, path string) (registrySchema, error) {
	var v registrySchema
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return v, fmt.Errorf("invalid schema registry request")
	}
	req.Header.Set("Accept", "application/vnd.schemaregistry.v1+json")
	if s.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+s.bearer)
	} else if s.username != "" {
		req.SetBasicAuth(s.username, s.password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return v, fmt.Errorf("schema registry request failed (check connection, TLS and timeout)")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return v, fmt.Errorf("schema registry returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(b) > maxBody {
		return v, fmt.Errorf("schema registry response unreadable or exceeds 4 MiB")
	}
	if json.Unmarshal(b, &v) != nil {
		return v, fmt.Errorf("invalid schema registry response")
	}
	if v.Type != "" && v.Type != "AVRO" {
		return v, fmt.Errorf("schema registry schema is not AVRO")
	}
	if len(v.References) > 0 {
		return v, fmt.Errorf("external Avro schema references are not supported")
	}
	return v, nil
}
func (s *kafkaRegistry) compile(v registrySchema) (*goavro.Codec, error) {
	c, err := goavro.NewCodec(v.Schema)
	if err != nil {
		return nil, fmt.Errorf("schema registry returned an invalid or unsupported Avro schema")
	}
	return c, nil
}
func (s *kafkaRegistry) byID(ctx context.Context, id uint32) (*goavro.Codec, error) {
	if c := s.codecs[id]; c != nil {
		return c, nil
	}
	v, err := s.get(ctx, "/schemas/ids/"+strconv.FormatUint(uint64(id), 10))
	if err != nil {
		return nil, err
	}
	c, err := s.compile(v)
	if err == nil {
		if len(s.codecs) >= 128 {
			clear(s.codecs)
		}
		s.codecs[id] = c
	}
	return c, err
}
func kafkaFormat(r config.Request, part string) (string, error) {
	def := "text"
	if r.Action == "consume" {
		def = "auto"
	}
	f := r.String(part+"_format", def)
	if f != "text" && f != "avro" && !(f == "auto" && r.Action == "consume") {
		return "", fmt.Errorf("%s_format must be text or avro (also auto for consume)", part)
	}
	return f, nil
}
func kafkaEncode(ctx context.Context, r config.Request, s *kafkaRegistry, part string) ([]byte, error) {
	f, err := kafkaFormat(r, part)
	if err != nil {
		return nil, err
	}
	data := []byte(r.String(part, ""))
	if len(data) > maxBody {
		return nil, fmt.Errorf("%s exceeds 4 MiB", part)
	}
	if f == "text" {
		return data, nil
	}
	if s == nil {
		return nil, fmt.Errorf("Avro %s requires schema_registry_url", part)
	}
	subject := r.String(part+"_subject", "")
	if subject == "" {
		return nil, fmt.Errorf("Avro %s requires %s_subject", part, part)
	}
	version := r.String(part+"_version", "latest")
	if version != "latest" {
		n, e := strconv.ParseUint(version, 10, 31)
		if e != nil || n == 0 {
			return nil, fmt.Errorf("%s_version must be latest or a positive integer", part)
		}
	}
	v, err := s.get(ctx, "/subjects/"+url.PathEscape(subject)+"/versions/"+version)
	if err != nil {
		return nil, err
	}
	if v.ID == 0 {
		return nil, fmt.Errorf("schema registry returned invalid schema ID")
	}
	c, err := s.compile(v)
	if err != nil {
		return nil, err
	}
	native, rest, err := c.NativeFromTextual(data)
	if err != nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("%s is not valid Avro JSON for the selected schema", part)
	}
	frame := make([]byte, 5)
	binary.BigEndian.PutUint32(frame[1:], v.ID)
	// Confluent's primitive bytes schema omits Avro's normal length prefix.
	if c.CanonicalSchema() == `"bytes"` {
		frame = append(frame, native.([]byte)...)
	} else {
		frame, err = c.BinaryFromNative(frame, native)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot encode Avro %s", part)
	}
	if len(frame) > maxBody {
		return nil, fmt.Errorf("encoded %s exceeds 4 MiB", part)
	}
	return frame, nil
}
func kafkaDecode(ctx context.Context, r config.Request, s *kafkaRegistry, part string, data []byte) (any, error) {
	f, err := kafkaFormat(r, part)
	if err != nil {
		return nil, err
	}
	if len(data) > maxBody {
		return nil, fmt.Errorf("%s exceeds 4 MiB", part)
	}
	if data == nil {
		return nil, nil
	} // Preserve Kafka tombstones.
	framed := len(data) > 0 && data[0] == 0
	if f == "avro" || (f == "auto" && framed) {
		if len(data) < 5 || !framed {
			return nil, fmt.Errorf("%s is not a Confluent Avro frame", part)
		}
		if s == nil {
			return nil, fmt.Errorf("%s appears schema-registry encoded; configure schema_registry_url or explicitly select text format", part)
		}
		id := binary.BigEndian.Uint32(data[1:5])
		if id == 0 {
			return nil, fmt.Errorf("invalid %s schema ID", part)
		}
		c, err := s.byID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("decode %s schema ID %d: %w", part, id, err)
		}
		var native any
		if c.CanonicalSchema() == `"bytes"` {
			native = data[5:]
		} else {
			var rest []byte
			native, rest, err = c.NativeFromBinary(data[5:])
			if err != nil || len(rest) != 0 {
				return nil, fmt.Errorf("invalid Avro %s payload for schema ID %d", part, id)
			}
		}
		text, err := c.TextualFromNative(nil, native)
		if err != nil {
			return nil, fmt.Errorf("cannot render Avro %s", part)
		}
		var value any
		d := json.NewDecoder(bytes.NewReader(text))
		d.UseNumber()
		if err = d.Decode(&value); err != nil {
			return nil, fmt.Errorf("cannot render Avro %s as JSON", part)
		}
		return value, nil
	}
	// Avoid irreversible replacement of arbitrary bytes with UTF-8 replacement chars.
	if !utf8.Valid(data) {
		return map[string]any{"encoding": "base64", "data": base64.StdEncoding.EncodeToString(data)}, nil
	}
	if part == "value" {
		var v any
		if json.Unmarshal(data, &v) == nil {
			return v, nil
		}
	}
	return string(data), nil
}
