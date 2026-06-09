package cc_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	cc "github.com/alexioschen/cc-connect/goagent"
)

func TestFuncToolInputSchemaRequiredRespectsOmitEmptyAndPointers(t *testing.T) {
	type schemaInput struct {
		Query      string  `json:"query" desc:"required query"`
		Limit      int     `json:"limit,omitempty" desc:"optional limit"`
		Recursive  *bool   `json:"recursive" desc:"optional recursive flag"`
		Defaulted  string  `json:",omitempty" desc:"optional default-named field"`
		Multiplier float64 `json:"multiplier" desc:"required multiplier"`
		Ignored    string  `json:"-"`
	}

	tool := cc.NewFuncTool("schema_test", "schema test", func(context.Context, schemaInput) (string, error) {
		return "ok", nil
	})

	var schema struct {
		Properties map[string]map[string]string `json:"properties"`
		Required   []string                     `json:"required"`
	}
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	if !slices.Contains(schema.Required, "query") {
		t.Fatalf("expected query to be required, got %v", schema.Required)
	}
	if !slices.Contains(schema.Required, "multiplier") {
		t.Fatalf("expected multiplier to be required, got %v", schema.Required)
	}
	for _, optional := range []string{"limit", "recursive", "defaulted"} {
		if slices.Contains(schema.Required, optional) {
			t.Fatalf("expected %s to be optional, got required %v", optional, schema.Required)
		}
		if _, ok := schema.Properties[optional]; !ok {
			t.Fatalf("expected %s property to exist", optional)
		}
	}
	if _, ok := schema.Properties["ignored"]; ok {
		t.Fatal("expected json:\"-\" field to be omitted")
	}
	if got := schema.Properties["recursive"]["type"]; got != "boolean" {
		t.Fatalf("expected pointer bool schema type boolean, got %q", got)
	}
}
