package main

import (
	"github.com/Live-yum/iotools/internal/engine"
	"strings"
)

type listFlag []string

func (v *listFlag) String() string     { return strings.Join(*v, ",") }
func (v *listFlag) Set(s string) error { *v = append(*v, s); return nil }

type optionalFlag struct {
	set   bool
	value string
}

func (v *optionalFlag) String() string     { return v.value }
func (v *optionalFlag) Set(s string) error { v.set = true; v.value = s; return nil }
func (v *optionalFlag) pointer() *string {
	if !v.set {
		return nil
	}
	return &v.value
}
func makeOverrides(fields, headers, query, form listFlag, body, bearer, basic optionalFlag) engine.HTTPOverrides {
	return engine.HTTPOverrides{Fields: fields, Headers: headers, Query: query, Form: form, Body: body.pointer(), Bearer: bearer.pointer(), Basic: basic.pointer()}
}
