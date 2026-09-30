package engine

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"
	"github.com/gopcua/opcua/uacp"
)

// OPC UA discovery is untrusted metadata. Session establishment independently
// validates the advertised certificate before any identity token is transmitted.
func runOPCUASession(ctx context.Context, r config.Request, emit Emit) error {
	switch r.Action {
	case "browse-path", "method-arguments", "discover", "browse", "references", "attributes", "read", "write", "call", "subscribe":
	default:
		return unsupported(r, "discover", "browse", "references", "attributes", "read", "write", "call", "subscribe")
	}
	if err := validateOPCUAOperation(r); err != nil {
		return err
	}
	address, err := url.Parse(r.Endpoint)
	if err != nil || address.Scheme != "opc.tcp" || address.Hostname() == "" || address.User != nil {
		return fmt.Errorf("OPC UA requires an opc.tcp URL without embedded credentials")
	}
	endpoints, err := opcua.GetEndpoints(ctx, r.Endpoint, opcua.Dialer(isolatedOPCUADialer()), opcua.AutoReconnect(false), opcua.DialTimeout(5*time.Second), opcua.RequestTimeout(10*time.Second), opcua.MaxMessageSize(16<<20))
	if err != nil {
		return fmt.Errorf("OPC UA discovery failed: %w", err)
	}
	if r.Action == "discover" {
		for _, ep := range endpoints {
			if ep == nil {
				continue
			}
			certificate := ep.ServerCertificate
			if chain, e := x509.ParseCertificates(certificate); e == nil && len(chain) > 0 {
				certificate = chain[0].Raw
			}
			sum := sha256.Sum256(certificate)
			uri := ""
			if ep.Server != nil {
				uri = ep.Server.ApplicationURI
			}
			tokens := []string{}
			for _, token := range ep.UserIdentityTokens {
				if token != nil {
					tokens = append(tokens, token.TokenType.String())
				}
			}
			send(emit, "endpoint", map[string]any{"url": ep.EndpointURL, "security_policy": ep.SecurityPolicyURI, "security_mode": ep.SecurityMode.String(), "certificate_sha256": hex.EncodeToString(sum[:]), "application_uri": uri, "trusted": false, "identity_tokens": tokens})
		}
		return nil
	}
	options, ep, err := opcuaOptions(r, endpoints, address.Hostname())
	if err != nil {
		return err
	}
	c, err := opcua.NewClient(r.Endpoint, options...)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = c.Close(cleanup)
	}()
	if err = c.Connect(ctx); err != nil {
		return fmt.Errorf("OPC UA connection failed: %w", err)
	}
	send(emit, "connected", map[string]any{"security_policy": ep.SecurityPolicyURI, "security_mode": ep.SecurityMode.String()})
	switch r.Action {
	case "browse-path":
		node, e := resolveUABrowsePath(ctx, c, r.String("browse_path", ""))
		if e != nil {
			return e
		}
		r.Params["node_id"] = node.String()
		send(emit, "path-resolved", map[string]any{"node_id": node.String(), "path": r.String("browse_path", "")})
		return opcuaBrowse(ctx, c, r, emit)
	case "method-arguments":
		return opcuaMethodArguments(ctx, c, r, emit)
	case "browse", "references":
		return opcuaBrowse(ctx, c, r, emit)
	case "read", "attributes":
		return opcuaReadAttributes(ctx, c, r, emit)
	case "write":
		n, err := ua.ParseNodeID(r.String("node_id", ""))
		if err != nil {
			return err
		}
		v, err := opcuaVariant(r.String("value_type", ""), r.Params["value"])
		if err != nil {
			return err
		}
		attribute, _ := opcuaAttribute(r.Params["attribute"])
		res, err := c.Write(ctx, &ua.WriteRequest{NodesToWrite: []*ua.WriteValue{{NodeID: n, AttributeID: attribute, Value: &ua.DataValue{EncodingMask: ua.DataValueValue, Value: v}}}})
		if err != nil {
			return err
		}
		if res == nil || len(res.Results) != 1 {
			return fmt.Errorf("incomplete OPC UA write response")
		}
		send(emit, "write", map[string]any{"node_id": n.String(), "status": res.Results[0].Error()})
		if res.Results[0] != ua.StatusOK {
			return res.Results[0]
		}
		return nil
	case "call":
		object, err := ua.ParseNodeID(r.String("object_id", ""))
		if err != nil {
			return err
		}
		method, err := ua.ParseNodeID(r.String("method_id", ""))
		if err != nil {
			return err
		}
		args, err := opcuaArguments(r.Params["arguments"])
		if err != nil {
			return err
		}
		res, err := c.Call(ctx, &ua.CallMethodRequest{ObjectID: object, MethodID: method, InputArguments: args})
		if err != nil {
			return err
		}
		if res == nil {
			return fmt.Errorf("empty method response")
		}
		values := make([]any, len(res.OutputArguments))
		for i, v := range res.OutputArguments {
			if v != nil {
				values[i] = v.Value()
			}
		}
		send(emit, "method", map[string]any{"status": res.StatusCode.Error(), "outputs": values, "input_statuses": res.InputArgumentResults})
		if res.StatusCode != ua.StatusOK {
			return res.StatusCode
		}
		for _, s := range res.InputArgumentResults {
			if s != ua.StatusOK {
				return s
			}
		}
		return nil
	case "subscribe":
		return opcuaSubscribe(ctx, c, r, emit)
	}
	return nil
}

func opcuaOptions(r config.Request, endpoints []*ua.EndpointDescription, host string) ([]opcua.Option, *ua.EndpointDescription, error) {
	policy := r.String("security_policy", "Basic256Sha256")
	modeName := r.String("security_mode", "SignAndEncrypt")
	mode := ua.MessageSecurityModeFromString(modeName)
	if policy != "Basic256Sha256" && policy != "None" {
		return nil, nil, fmt.Errorf("supported OPC UA policies are Basic256Sha256 and explicitly allowed None")
	}
	if mode != ua.MessageSecurityModeSignAndEncrypt && mode != ua.MessageSecurityModeSign && mode != ua.MessageSecurityModeNone {
		return nil, nil, fmt.Errorf("invalid OPC UA security_mode")
	}
	insecure := policy == "None" || mode == ua.MessageSecurityModeNone
	if insecure && (policy != "None" || mode != ua.MessageSecurityModeNone || !r.Bool("allow_insecure")) {
		return nil, nil, fmt.Errorf("None policy/mode must be paired and explicitly allow_insecure=true")
	}
	auth := r.String("auth", "anonymous")
	token := ua.UserTokenTypeAnonymous
	switch auth {
	case "anonymous":
	case "username":
		token = ua.UserTokenTypeUserName
	case "certificate":
		token = ua.UserTokenTypeCertificate
	default:
		return nil, nil, fmt.Errorf("auth must be anonymous, username or certificate")
	}
	if auth != "anonymous" && mode != ua.MessageSecurityModeSignAndEncrypt {
		return nil, nil, fmt.Errorf("identity credentials require SignAndEncrypt")
	}
	var ep *ua.EndpointDescription
	for _, candidate := range endpoints {
		if candidate == nil || candidate.SecurityMode != mode || candidate.SecurityPolicyURI != ua.SecurityPolicyURIPrefix+policy {
			continue
		}
		for _, identity := range candidate.UserIdentityTokens {
			if identity != nil && identity.TokenType == token {
				ep = candidate
				break
			}
		}
		if ep != nil {
			break
		}
	}
	if ep == nil {
		return nil, nil, fmt.Errorf("server does not advertise the requested security and authentication profile")
	}
	opts := []opcua.Option{opcua.Dialer(isolatedOPCUADialer()), opcua.AutoReconnect(false), opcua.DialTimeout(5 * time.Second), opcua.RequestTimeout(10 * time.Second), opcua.MaxMessageSize(16 << 20), opcua.SecurityFromEndpoint(ep, token)}
	if !insecure {
		if err := opcuaTrust(r, ep, host, time.Now()); err != nil {
			return nil, nil, err
		}
		cert, key := r.String("cert_file", ""), r.String("key_file", "")
		if cert == "" || key == "" {
			return nil, nil, fmt.Errorf("secure OPC UA requires client cert_file and key_file")
		}
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return nil, nil, fmt.Errorf("load OPC UA client identity: %w", err)
		}
		private, ok := pair.PrivateKey.(*rsa.PrivateKey)
		if !ok {
			return nil, nil, fmt.Errorf("OPC UA client key must be RSA")
		}
		opts = append(opts, opcua.Certificate(pair.Certificate[0]), opcua.PrivateKey(private))
	}
	switch auth {
	case "anonymous":
		opts = append(opts, opcua.AuthAnonymous())
	case "username":
		if r.String("username", "") == "" || r.String("password", "") == "" {
			return nil, nil, fmt.Errorf("username authentication requires username and password")
		}
		opts = append(opts, opcua.AuthUsername(r.String("username", ""), r.String("password", "")))
	case "certificate":
		pair, err := tls.LoadX509KeyPair(r.String("auth_cert_file", ""), r.String("auth_key_file", ""))
		if err != nil {
			return nil, nil, fmt.Errorf("load OPC UA user identity: %w", err)
		}
		private, ok := pair.PrivateKey.(*rsa.PrivateKey)
		if !ok {
			return nil, nil, fmt.Errorf("OPC UA user key must be RSA")
		}
		opts = append(opts, opcua.AuthCertificate(pair.Certificate[0]), opcua.AuthPrivateKey(private))
	}
	return opts, ep, nil
}

func opcuaTrust(r config.Request, ep *ua.EndpointDescription, host string, now time.Time) error {
	certs, err := x509.ParseCertificates(ep.ServerCertificate)
	if err != nil || len(certs) == 0 {
		return fmt.Errorf("server did not present a valid certificate chain")
	}
	leaf := certs[0]
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return fmt.Errorf("server certificate is expired or not yet valid")
	}
	if err = leaf.VerifyHostname(host); err != nil {
		return fmt.Errorf("server certificate hostname validation failed: %w", err)
	}
	if ep.Server == nil || ep.Server.ApplicationURI == "" {
		return fmt.Errorf("server application URI missing")
	}
	uriOK := false
	for _, uri := range leaf.URIs {
		uriOK = uriOK || uri.String() == ep.Server.ApplicationURI
	}
	if !uriOK {
		return fmt.Errorf("server application URI does not match certificate")
	}
	pin := strings.ReplaceAll(r.String("server_cert_sha256", ""), ":", "")
	caFile := r.String("ca_file", "")
	if pin == "" && caFile == "" {
		return fmt.Errorf("secure OPC UA requires server_cert_sha256 or ca_file; discovery is not trust")
	}
	if pin != "" {
		expected, e := hex.DecodeString(pin)
		if e != nil || len(expected) != sha256.Size {
			return fmt.Errorf("server_cert_sha256 must contain 64 hex digits")
		}
		actual := sha256.Sum256(leaf.Raw)
		if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
			return fmt.Errorf("server certificate pin mismatch")
		}
	}
	if caFile != "" {
		pem, e := os.ReadFile(caFile)
		if e != nil {
			return e
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return fmt.Errorf("ca_file contains no certificates")
		}
		intermediates := x509.NewCertPool()
		for _, cert := range certs[1:] {
			intermediates.AddCert(cert)
		}
		_, e = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: host, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
		if e != nil {
			return fmt.Errorf("server certificate CA validation failed: %w", e)
		}
	}
	return nil
}

func opcuaNodes(r config.Request) ([]*ua.NodeID, error) {
	names := r.Strings("node_ids")
	if len(names) == 0 {
		names = []string{r.String("node_id", "")}
	}
	if len(names) > 256 {
		return nil, fmt.Errorf("at most 256 node IDs per operation")
	}
	out := make([]*ua.NodeID, len(names))
	for i, n := range names {
		if n == "" {
			return nil, fmt.Errorf("node_id or node_ids is required")
		}
		id, e := ua.ParseNodeID(n)
		if e != nil {
			return nil, e
		}
		out[i] = id
	}
	return out, nil
}
func opcuaData(node string, v *ua.DataValue) map[string]any {
	var value any
	var typ any
	if v.Value != nil {
		value = v.Value.Value()
		typ = v.Value.Type()
	}
	return map[string]any{"node_id": node, "value": value, "value_type": typ, "status": v.Status.Error(), "source_timestamp": v.SourceTimestamp, "server_timestamp": v.ServerTimestamp}
}

type opcuaBrowser interface {
	Browse(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error)
	BrowseNext(context.Context, *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error)
}

func opcuaBrowse(ctx context.Context, c opcuaBrowser, r config.Request, emit Emit) error {
	node, err := ua.ParseNodeID(r.String("node_id", "i=85"))
	if err != nil {
		return err
	}
	limit := r.Int("max_references", 1000)
	if limit < 1 || limit > 100000 {
		return fmt.Errorf("max_references must be 1..100000")
	}
	direction, e := opcuaBrowseDirection(r)
	if e != nil {
		return e
	}
	var referenceType *ua.NodeID
	if id := r.String("reference_type", ""); id != "" {
		referenceType, e = ua.ParseNodeID(id)
		if e != nil {
			return e
		}
	}
	includeSubtypes := true
	if _, ok := r.Params["include_subtypes"]; ok {
		includeSubtypes = r.Bool("include_subtypes")
	}
	res, err := c.Browse(ctx, &ua.BrowseRequest{View: &ua.ViewDescription{}, RequestedMaxReferencesPerNode: 100, NodesToBrowse: []*ua.BrowseDescription{{NodeID: node, BrowseDirection: direction, ReferenceTypeID: referenceType, IncludeSubtypes: includeSubtypes, ResultMask: uint32(ua.BrowseResultMaskAll)}}})
	if err != nil {
		return err
	}
	if res == nil {
		return fmt.Errorf("empty browse response")
	}
	results := res.Results
	var continuation []byte
	count := 0
	defer func() {
		if len(continuation) > 0 {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = c.BrowseNext(cleanup, &ua.BrowseNextRequest{ReleaseContinuationPoints: true, ContinuationPoints: [][]byte{continuation}})
		}
	}()
	for {
		if len(results) != 1 || results[0] == nil {
			return fmt.Errorf("malformed browse response")
		}
		result := results[0]
		continuation = result.ContinuationPoint
		if result.StatusCode != ua.StatusOK {
			return result.StatusCode
		}
		for _, ref := range result.References {
			if count >= limit {
				return fmt.Errorf("browse reached max_references=%d; continuation released", limit)
			}
			send(emit, "reference", ref)
			count++
		}
		if len(continuation) == 0 {
			return nil
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		next, e := c.BrowseNext(ctx, &ua.BrowseNextRequest{ContinuationPoints: [][]byte{continuation}})
		if e != nil {
			return e
		}
		if next == nil {
			return fmt.Errorf("empty browse-next response")
		}
		results = next.Results
	}
}

func opcuaSubscribe(ctx context.Context, c *opcua.Client, r config.Request, emit Emit) error {
	nodes, err := opcuaNodes(r)
	if err != nil {
		return err
	}
	interval := r.Int("interval_ms", 1000)
	limit := r.Int("max_events", 10)
	if interval < 50 || interval > 60000 || limit < 1 || limit > 100000 {
		return fmt.Errorf("interval_ms must be 50..60000 and max_events 1..100000")
	}
	notifications := make(chan *opcua.PublishNotificationData, 64)
	sub, err := c.Subscribe(ctx, &opcua.SubscriptionParameters{Interval: time.Duration(interval) * time.Millisecond}, notifications)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = sub.Cancel(cleanup)
	}()
	items := make([]*ua.MonitoredItemCreateRequest, len(nodes))
	for i, n := range nodes {
		items[i] = opcua.NewMonitoredItemCreateRequestWithDefaults(n, ua.AttributeIDValue, uint32(i+1))
	}
	result, err := sub.Monitor(ctx, ua.TimestampsToReturnBoth, items...)
	if err != nil {
		return err
	}
	if result == nil || len(result.Results) != len(items) {
		return fmt.Errorf("incomplete monitor response")
	}
	for _, item := range result.Results {
		if item == nil || item.StatusCode != ua.StatusOK {
			return fmt.Errorf("server rejected monitored item")
		}
	}
	count := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message, ok := <-notifications:
			if !ok {
				return fmt.Errorf("subscription channel closed")
			}
			if message == nil {
				continue
			}
			if message.Error != nil {
				return message.Error
			}
			if data, ok := message.Value.(*ua.DataChangeNotification); ok {
				for _, item := range data.MonitoredItems {
					if item == nil || item.Value == nil || item.ClientHandle < 1 || int(item.ClientHandle) > len(nodes) {
						return fmt.Errorf("invalid monitored value")
					}
					send(emit, "notification", opcuaData(nodes[item.ClientHandle-1].String(), item.Value))
					if item.Value.Status != ua.StatusOK {
						return item.Value.Status
					}
					count++
					if count >= limit {
						return nil
					}
				}
			}
		}
	}
}

func opcuaArguments(raw any) ([]*ua.Variant, error) {
	if raw == nil {
		return nil, nil
	}
	rows, ok := raw.([]any)
	if !ok || len(rows) > 64 {
		return nil, fmt.Errorf("arguments must be a list of at most 64 {type,value} entries")
	}
	args := make([]*ua.Variant, len(rows))
	for i, row := range rows {
		m, ok := row.(map[string]any)
		if !ok || len(m) != 2 {
			return nil, fmt.Errorf("method argument must have exactly type and value")
		}
		kind, ok := m["type"].(string)
		if !ok {
			return nil, fmt.Errorf("method argument type is required")
		}
		v, err := opcuaVariant(kind, m["value"])
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	return args, nil
}
func opcuaVariant(kind string, raw any) (*ua.Variant, error) {
	if strings.HasSuffix(kind, "[]") {
		rows, ok := raw.([]any)
		if !ok || len(rows) > 10000 {
			return nil, fmt.Errorf("typed array requires at most 10000 entries")
		}
		base := strings.TrimSuffix(kind, "[]")
		sample, err := opcuaScalar(base, opcuaZero(base))
		if err != nil {
			return nil, err
		}
		array := reflect.MakeSlice(reflect.SliceOf(reflect.TypeOf(sample)), 0, len(rows))
		for _, row := range rows {
			v, err := opcuaScalar(base, row)
			if err != nil {
				return nil, err
			}
			array = reflect.Append(array, reflect.ValueOf(v))
		}
		return ua.NewVariant(array.Interface())
	}
	value, err := opcuaScalar(kind, raw)
	if err != nil {
		return nil, err
	}
	return ua.NewVariant(value)
}
func opcuaZero(kind string) any {
	switch kind {
	case "Boolean":
		return false
	case "String", "ByteString":
		return ""
	case "NodeId", "NodeID":
		return "i=0"
	case "Guid", "GUID":
		return "00000000-0000-0000-0000-000000000000"
	case "DateTime":
		return "2000-01-01T00:00:00Z"
	case "LocalizedText":
		return map[string]any{"text": ""}
	case "QualifiedName":
		return map[string]any{"name": "", "namespace": 0}
	default:
		return 0
	}
}
func opcuaScalar(kind string, raw any) (any, error) {
	if raw == nil {
		return nil, fmt.Errorf("typed OPC UA value is required")
	}
	text := fmt.Sprint(raw)
	switch kind {
	case "LocalizedText":
		m, ok := raw.(map[string]any)
		if !ok || len(m) > 2 {
			return nil, fmt.Errorf("LocalizedText 要求 {text,locale}")
		}
		value, ok := m["text"].(string)
		if !ok {
			return nil, fmt.Errorf("LocalizedText.text 必须是字符串")
		}
		locale := ""
		if x, exists := m["locale"]; exists {
			locale, ok = x.(string)
			if !ok {
				return nil, fmt.Errorf("LocalizedText.locale 必须是字符串")
			}
		}
		for key := range m {
			if key != "text" && key != "locale" {
				return nil, fmt.Errorf("未知 LocalizedText 字段")
			}
		}
		return ua.NewLocalizedTextWithLocale(value, locale), nil
	case "QualifiedName":
		m, ok := raw.(map[string]any)
		if !ok || len(m) > 2 {
			return nil, fmt.Errorf("QualifiedName 要求 {name,namespace}")
		}
		value, ok := m["name"].(string)
		if !ok {
			return nil, fmt.Errorf("QualifiedName.name 必须是字符串")
		}
		ns := int64(0)
		if x, exists := m["namespace"]; exists {
			var e error
			ns, e = exactInt(x)
			if e != nil || ns < 0 || ns > 65535 {
				return nil, fmt.Errorf("QualifiedName.namespace 超出 0..65535")
			}
		}
		for key := range m {
			if key != "name" && key != "namespace" {
				return nil, fmt.Errorf("未知 QualifiedName 字段")
			}
		}
		return &ua.QualifiedName{NamespaceIndex: uint16(ns), Name: value}, nil
	case "Boolean":
		switch value := raw.(type) {
		case bool:
			return value, nil
		case string:
			v, e := strconv.ParseBool(value)
			return v, e
		}
		return nil, fmt.Errorf("Boolean requires true or false")
	case "String":
		value, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("String requires text")
		}
		return value, nil
	case "NodeId", "NodeID":
		value, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("NodeId需要文本")
		}
		return ua.ParseNodeID(value)
	case "Guid", "GUID":
		value, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("Guid需要文本")
		}
		guid := ua.NewGUID(value)
		if guid == nil {
			return nil, fmt.Errorf("无效Guid")
		}
		return guid, nil
	case "ByteString":
		value, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("ByteString requires base64 text")
		}
		return base64.StdEncoding.DecodeString(value)
	case "DateTime":
		return time.Parse(time.RFC3339Nano, text)
	case "Float", "Double":
		bits := 64
		if kind == "Float" {
			bits = 32
		}
		v, e := strconv.ParseFloat(text, bits)
		if e != nil {
			return nil, e
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("floating values must be finite")
		}
		if kind == "Float" {
			return float32(v), nil
		}
		return v, nil
	case "SByte", "Int16", "Int32", "Int64":
		bits := map[string]int{"SByte": 8, "Int16": 16, "Int32": 32, "Int64": 64}[kind]
		v, e := strconv.ParseInt(text, 10, bits)
		if e != nil {
			return nil, e
		}
		switch kind {
		case "SByte":
			return int8(v), nil
		case "Int16":
			return int16(v), nil
		case "Int32":
			return int32(v), nil
		default:
			return v, nil
		}
	case "Byte", "UInt16", "UInt32", "UInt64":
		bits := map[string]int{"Byte": 8, "UInt16": 16, "UInt32": 32, "UInt64": 64}[kind]
		v, e := strconv.ParseUint(text, 10, bits)
		if e != nil {
			return nil, e
		}
		switch kind {
		case "Byte":
			return uint8(v), nil
		case "UInt16":
			return uint16(v), nil
		case "UInt32":
			return uint32(v), nil
		default:
			return v, nil
		}
	default:
		return nil, fmt.Errorf("unsupported value_type %q; use Boolean, String, ByteString(base64), DateTime(RFC3339), signed/unsigned integer widths, Float, Double or typed []", kind)
	}
}

// gopcua's default dialer shares ClientACK globally; each concurrent client must
// own its handshake options before applying MaxMessageSize to avoid data races.
func isolatedOPCUADialer() *uacp.Dialer {
	d := opcua.DefaultDialer()
	ack := *d.ClientACK
	d.ClientACK = &ack
	return d
}
