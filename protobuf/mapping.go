package protobuf

import (
	"fmt"
	"strings"
)

// ProtoType represents a protobuf type. It can optionally have a
// package and it may require an import to work.
type ProtoType struct {
	Package  string
	Basic    bool
	Name     string
	Import   string
	GoImport string
}

// Type returns the type representation of the protobuf type.
func (t *ProtoType) Type() Type {
	if t.Basic {
		return NewBasic(t.Name)
	}
	return NewNamed(t.Package, t.Name)
}

// TypeMappings is a mapping between Go types and protobuf types.
// The names of the Go types can have packages. For example: "time.Time" is a
// valid name. "foo.bar/baz.Qux" is a valid type name as well.
type TypeMappings map[string]*ProtoType

var DefaultMappings = TypeMappings{
	"float64": &ProtoType{Name: "double", Basic: true},
	"float32": &ProtoType{Name: "float", Basic: true},
	"int32":   &ProtoType{Name: "int32", Basic: true},
	"int64":   &ProtoType{Name: "int64", Basic: true},
	"uint32":  &ProtoType{Name: "uint32", Basic: true},
	"uint64":  &ProtoType{Name: "uint64", Basic: true},
	"bool":    &ProtoType{Name: "bool", Basic: true},
	"string":  &ProtoType{Name: "string", Basic: true},
	"uint8":   &ProtoType{Name: "uint32", Basic: true},
	"int8":    &ProtoType{Name: "int32", Basic: true},
	"byte":    &ProtoType{Name: "uint32", Basic: true},
	"uint16":  &ProtoType{Name: "uint32", Basic: true},
	"int16":   &ProtoType{Name: "int32", Basic: true},
	"int":     &ProtoType{Name: "int32", Basic: true},
	"uint":    &ProtoType{Name: "uint32", Basic: true},
	"uintptr": &ProtoType{Name: "uint64", Basic: true},
	"rune":    &ProtoType{Name: "int32", Basic: true},
	"time.Time": &ProtoType{
		Name:     "Timestamp",
		Package:  "google.protobuf",
		Import:   "google/protobuf/timestamp.proto",
		GoImport: "github.com/gogo/protobuf/types",
	},
	"time.Duration": &ProtoType{Name: "int64", Basic: true},
}

// ToGoOutPath returns the set of import mappings for the --go_out family of options.
// For more info see src-d/proteus#41
func (t TypeMappings) ToGoOutPath() string {
	var strs []string
	for _, value := range t {
		if value.Import != "" && value.GoImport != "" {
			strs = append(strs, fmt.Sprintf("M%s=%s", value.Import, value.GoImport))
		}
	}

	return strings.Join(strs, ",")
}
