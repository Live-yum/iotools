package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlumberImportReferencesFoldersAndValidation(t *testing.T) {
	source := []byte(`name: Demo
.base:
  headers: {Accept: application/json}
profiles:
  local:
    default: true
    data: {host: 'http://127.0.0.1:18080', count: 3}
requests:
  folder:
    name: Group
    requests:
      first:
        $ref: '#/.base'
        method: GET
        url: '{{ host }}/first'
      second:
        method: POST
        url: '{{ host }}/second'
        body: {type: json, data: {count: '{{ count | integer() }}'}}
`)
	c, e := ParseCollection(source)
	if e != nil {
		t.Fatal(e)
	}
	if len(c.Requests) != 2 || c.Requests[0].Name != "Group / first" || c.DefaultProfile != "local" {
		t.Fatalf("bad conversion: %#v", c)
	}
	if c.Requests[0].Params["headers"].(map[string]any)["Accept"] != "application/json" {
		t.Fatal("local reference lost")
	}
	p := filepath.Join(t.TempDir(), "slumber.yml")
	if e = os.WriteFile(p, source, 0600); e != nil {
		t.Fatal(e)
	}
	loaded, raw, e := LoadCollection(p)
	if e != nil || string(raw) != string(source) || loaded.SourcePath != p {
		t.Fatal("source identity changed", e)
	}
	for _, invalid := range []string{
		"requests: {x: {method: GET, url: 'http://127.0.0.1', surprise: true}}",
		"requests: {x: {method: GET, url: 'http://127.0.0.1', '$ref': 'https://example.invalid/x'}}",
		".base: {'$ref': '#/.base'}\nrequests: {x: {'$ref': '#/.base'}}",
		"requests: {folder: {requests: {folder: {method: GET, url: 'http://127.0.0.1'}}}}",
		"requests: {}\n---\nrequests: {}",
		"profiles: {a: {default: true}, b: {default: true}}\nrequests: {}",
	} {
		if _, e = ParseCollection([]byte(invalid)); e == nil {
			t.Fatalf("invalid collection accepted: %s", invalid)
		}
	}
}
func TestExternalHTTPImports(t *testing.T) {
	rest := `@host = http://127.0.0.1:18080
### List users
GET {{host}}/users
Accept: application/json

### Create user
POST {{host}}/users
Content-Type: application/json

{"name":"demo"}
`
	c, e := ImportCollection([]byte(rest), "rest")
	if e != nil || len(c.Requests) != 2 || c.Requests[1].Params["body"] != `{"name":"demo"}` {
		t.Fatal(c, e)
	}
	insomnia := `{"resources":[{"_type":"environment","_id":"env_local","name":"Local","data":{"host":"http://127.0.0.1:18080"}},{"_type":"request","_id":"req_one","name":"List","method":"GET","url":"{{ _.host }}/users","headers":[{"name":"Accept","value":"application/json"}],"parameters":[{"name":"tag","value":"a"},{"name":"tag","value":"b"}]}]}`
	c, e = ImportCollection([]byte(insomnia), "insomnia")
	if e != nil || len(c.Requests) != 1 || c.Requests[0].Endpoint != "{{ host }}/users" {
		t.Fatal(c, e)
	}
	openapi := `openapi: 3.0.3
servers: [{url: 'http://127.0.0.1:18080'}]
paths:
  /users/{id}:
    parameters: [{in: path, name: id, schema: {type: integer, default: 42}}]
    get:
      operationId: get_user
      parameters: [{in: query, name: verbose, example: true}]
      security: [{bearerAuth: []}]
components:
  securitySchemes:
    bearerAuth: {type: http, scheme: bearer}
`
	c, e = ImportCollection([]byte(openapi), "openapi")
	if e != nil || len(c.Requests) != 1 || c.Requests[0].Params["bearer"] != `{{ env('IOTOOLS_TOKEN') }}` {
		t.Fatal(c, e)
	}
	if c.Profiles["imported"]["id"] != "42" {
		t.Fatal("path default missing")
	}
	for format, source := range map[string]string{"openapi": "openapi: 2.0\npaths: {}", "rest": "GET http://127.0.0.1\n\n> {% script %}", "insomnia": `{"resources":[{"_type":"request","preRequestScript":"alert(1)"}]}`} {
		if _, e = ImportCollection([]byte(source), format); e == nil {
			t.Fatalf("unsupported import accepted: %s", format)
		}
	}
}
func TestSlumberV3Migration(t *testing.T) {
	source := `profiles:
  local:
    default: true
    data: {host: 'http://127.0.0.1:18080'}
chains:
  token:
    source: !env {variable: IOTOOLS_V3_TEST}
    trim: both
    sensitive: true
requests:
  group: !folder
    requests:
      demo: !request
        method: POST
        url: '{{host}}/users'
        authentication: !bearer '{{chains.token}}'
        query: ['item={{host}}', 'tag=a', 'tag=b']
        body: !json {name: '{{env.IOTOOLS_V3_TEST}}'}
`
	c, e := ImportCollection([]byte(source), "v3")
	if e != nil {
		t.Fatal(e)
	}
	if len(c.Requests) != 1 {
		t.Fatal(c)
	}
	t.Setenv("IOTOOLS_V3_TEST", " demo ")
	w := testWorkflow()
	w.collection = c
	w.profile = "local"
	w.vars = c.Profiles["local"]
	w.current = c.Requests[0]
	r, e := w.renderRequest(c.Requests[0])
	if e != nil {
		t.Fatal(e)
	}
	if r.Params["bearer"] != "demo" || r.Endpoint != "http://127.0.0.1:18080/users" {
		t.Fatal(r)
	}
	q := r.Params["query"].(map[string]any)
	if q["item"] != "http://127.0.0.1:18080" || len(q["tag"].([]any)) != 2 {
		t.Fatal(q)
	}
	if _, e = ImportCollection([]byte(strings.ReplaceAll(source, "!env {variable: IOTOOLS_V3_TEST}", "!command {command: [echo, hello]}")), "v3"); e == nil {
		t.Fatal("command chain silently converted")
	}
}
func TestHTTPValidationRejectsAmbiguousBodies(t *testing.T) {
	c, e := ParseCollection([]byte("version: 1\nrequests:\n- id: x\n  protocol: http\n  action: POST\n  endpoint: http://127.0.0.1:1\n  params: {body: x, json: {x: 1}}"))
	if e != nil {
		t.Fatal(e)
	}
	if e = RunCollection(context.Background(), c, c.Requests[0], "", true, nil); e == nil || !strings.Contains(e.Error(), "mutually") {
		t.Fatal(e)
	}
}
