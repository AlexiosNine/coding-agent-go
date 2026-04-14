package cc

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Tool is the interface for executable tools that the agent can call.
type Tool interface {
	// Name returns the unique identifier for this tool.
	Name() string
	// Description returns a human-readable description of what the tool does.
	Description() string
	// InputSchema returns the JSON Schema describing the tool's input parameters.
	InputSchema() json.RawMessage
	// Execute runs the tool with the given JSON input and returns the result.
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

// FuncTool wraps a Go function as a Tool.
// T must be a struct type; its fields are used to generate the JSON Schema.
type FuncTool[T any] struct {
	name   string
	desc   string
	fn     func(ctx context.Context, input T) (string, error)
	schema json.RawMessage
}

// NewFuncTool creates a Tool from a typed Go function.
// The input type T must be a struct. Field tags `json` control parameter names,
// and `desc` tags provide parameter descriptions.
//
// Example:
//
//	type MathInput struct {
//	    A int `json:"a" desc:"first number"`
//	    B int `json:"b" desc:"second number"`
//	}
//	tool := cc.NewFuncTool("add", "Add two numbers", func(ctx context.Context, in MathInput) (string, error) {
//	    return fmt.Sprintf("%d", in.A+in.B), nil
//	})
func NewFuncTool[T any](name, desc string, fn func(ctx context.Context, input T) (string, error)) *FuncTool[T] {
	schema := generateSchema[T]()
	return &FuncTool[T]{name: name, desc: desc, fn: fn, schema: schema}
}

func (t *FuncTool[T]) Name() string                { return t.name }
func (t *FuncTool[T]) Description() string          { return t.desc }
func (t *FuncTool[T]) InputSchema() json.RawMessage { return t.schema }

func (t *FuncTool[T]) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var v T
	if err := json.Unmarshal(input, &v); err != nil {
		return "", fmt.Errorf("unmarshal tool input: %w", err)
	}
	return t.fn(ctx, v)
}

// generateSchema builds a JSON Schema from struct T's fields.
func generateSchema[T any]() json.RawMessage {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	properties := make(map[string]map[string]string)
	var required []string

	if t.Kind() == reflect.Struct {
		for i := range t.NumField() {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}

			name := field.Tag.Get("json")
			if name == "" || name == "-" {
				name = strings.ToLower(field.Name)
			}
			// Strip json tag options like ",omitempty"
			if idx := strings.Index(name, ","); idx != -1 {
				name = name[:idx]
			}

			prop := map[string]string{
				"type": goTypeToJSONType(field.Type),
			}
			if desc := field.Tag.Get("desc"); desc != "" {
				prop["description"] = desc
			}
			properties[name] = prop
			required = append(required, name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}

	data, _ := json.Marshal(schema)
	return data
}

func goTypeToJSONType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Bool:
		return "boolean"
	case reflect.Slice, reflect.Array:
		return "array"
	default:
		return "string"
	}
}
