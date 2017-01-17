package protobuf

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/src-d/proteus/resolver"
	"github.com/src-d/proteus/scanner"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestToLowerSnakeCase(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"fooBarBaz", "foo_bar_baz"},
		{"FooBarBaz", "foo_bar_baz"},
		{"foo1barBaz", "foo1bar_baz"},
		{"fooBAR", "foo_bar"},
		{"FBar", "fbar"},
	}

	for _, c := range cases {
		require.Equal(t, c.expected, toLowerSnakeCase(c.input))
	}
}

func TestToUpperSnakeCase(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"FooBarBaz", "FOO_BAR_BAZ"},
		{"fooBarBaz", "FOO_BAR_BAZ"},
		{"foo1barBaz", "FOO1BAR_BAZ"},
	}

	for _, c := range cases {
		require.Equal(t, c.expected, toUpperSnakeCase(c.input))
	}
}

func TestIsByteSlice(t *testing.T) {
	cases := []struct {
		t      scanner.Type
		result bool
	}{
		{scanner.NewBasic("byte"), false},
		{repeated(scanner.NewBasic("byte")), true},
		{scanner.NewNamed("foo", "Bar"), false},
	}

	for _, c := range cases {
		require.Equal(t, c.result, isByteSlice(c.t))
	}
}

func TestToProtobufPkg(t *testing.T) {
	cases := []struct {
		path string
		pkg  string
	}{
		{"foo", "foo"},
		{"net/url", "net.url"},
		{"github.com/foo/bar", "github.com.foo.bar"},
		{"github.cóm/fòo/bar", "github.com.foo.bar"},
		{"gopkg.in/go-foo/foo.v1", "gopkg.in.gofoo.foo.v1"},
	}

	for _, c := range cases {
		require.Equal(t, c.pkg, toProtobufPkg(c.path))
	}
}

type TransformerSuite struct {
	suite.Suite
	t *Transformer
}

func (s *TransformerSuite) SetupTest() {
	s.t = NewTransformer()
	s.t.SetMappings(TypeMappings{
		"url.URL":       &ProtoType{Name: "string", Basic: true},
		"time.Duration": &ProtoType{Name: "uint64", Basic: true},
	})
	s.t.SetMappings(nil)
	s.NotNil(s.t.mappings)
}

func (s *TransformerSuite) TestFindMapping() {
	cases := []struct {
		name         string
		protobufType string
		isNil        bool
	}{
		{"foo.Bar", "", true},
		{"url.URL", "string", false},
		{"time.Duration", "uint64", false},
		{"time.Time", "Timestamp", false},
	}

	for _, c := range cases {
		t := s.t.findMapping(c.name)
		if c.isNil {
			s.Nil(t)
		} else {
			s.NotNil(t)
			s.Equal(c.protobufType, t.Name)
		}
	}
}

func (s *TransformerSuite) TestMappingDecorators() {
	s.t.SetMappings(TypeMappings{
		"int": &ProtoType{
			Name:  "int64",
			Basic: true,
			Decorators: NewDecorators(
				func(p *Package, m *Message, f *Field) {
					f.Options["greeting"] = NewStringValue("hola")
				},
			),
		},
	})

	f := s.t.transformField(&Package{}, &Message{}, &scanner.Field{
		Name: "MyField",
		Type: scanner.NewBasic("int"),
	}, 1)

	s.Equal(NewStringValue("hola"), f.Options["greeting"], "option was added")
}

func (s *TransformerSuite) TestTransformType() {
	cases := []struct {
		typ      scanner.Type
		expected Type
		imported string
	}{
		{
			scanner.NewNamed("time", "Time"),
			NewNamed("google.protobuf", "Timestamp"),
			"google/protobuf/timestamp.proto",
		},
		{
			scanner.NewNamed("foo", "Bar"),
			NewNamed("foo", "Bar"),
			"foo/generated.proto",
		},
		{
			scanner.NewBasic("string"),
			NewBasic("string"),
			"",
		},
		{
			scanner.NewMap(
				scanner.NewBasic("string"),
				scanner.NewBasic("int64"),
			),
			NewMap(NewBasic("string"), NewBasic("int64")),
			"",
		},
		{
			repeated(scanner.NewBasic("int")),
			NewBasic("int32"),
			"",
		},
	}

	for _, c := range cases {
		var pkg Package
		t := s.t.transformType(&pkg, c.typ, &Message{}, &Field{})
		s.Equal(c.expected, t)

		if c.imported != "" {
			s.Equal(1, len(pkg.Imports))
			s.Equal(c.imported, pkg.Imports[0])
		}
	}
}

func (s *TransformerSuite) TestTransformField() {
	cases := []struct {
		name     string
		typ      scanner.Type
		expected *Field
	}{
		{
			"Foo",
			scanner.NewBasic("int"),
			&Field{Name: "foo", Type: NewBasic("int32"), Options: make(Options)},
		},
		{
			"Bar",
			repeated(scanner.NewBasic("byte")),
			&Field{Name: "bar", Type: NewBasic("bytes"), Options: make(Options)},
		},
		{
			"BazBar",
			repeated(scanner.NewBasic("int")),
			&Field{Name: "baz_bar", Type: NewBasic("int32"), Repeated: true, Options: make(Options)},
		},
		{
			"CustomID",
			scanner.NewBasic("int"),
			&Field{Name: "custom_id", Type: NewBasic("int32"), Options: Options{"(gogoproto.customname)": NewStringValue("CustomID")}},
		},
		{
			"Invalid",
			scanner.NewBasic("complex64"),
			nil,
		},
	}

	for _, c := range cases {
		f := s.t.transformField(&Package{}, &Message{}, &scanner.Field{
			Name: c.name,
			Type: c.typ,
		}, 0)
		s.Equal(c.expected, f, c.name)
	}
}

func (s *TransformerSuite) TestTransformStruct() {
	st := &scanner.Struct{
		Name: "Foo",
		Fields: []*scanner.Field{
			{
				Name: "Invalid",
				Type: scanner.NewBasic("complex64"),
			},
			{
				Name: "Bar",
				Type: scanner.NewBasic("string"),
			},
		},
	}

	msg := s.t.transformStruct(&Package{}, st)
	s.Equal("Foo", msg.Name)
	s.Equal(1, len(msg.Fields), "should have one field")
	s.Equal(2, msg.Fields[0].Pos)
	s.Equal(0, len(msg.Fields[0].Options))
	s.Equal(1, len(msg.Reserved), "should have reserved field")
	s.Equal(uint(1), msg.Reserved[0])
	s.Equal(NewLiteralValue("true"), msg.Options["(gogoproto.drop_type_declaration)"], "should drop declaration by default")
}

func (s *TransformerSuite) TestTransformFuncMultiple() {
	fn := &scanner.Func{
		Name: "DoFoo",
		Input: []scanner.Type{
			scanner.NewNamed("foo", "Bar"),
			scanner.NewBasic("int"),
		},
		Output: []scanner.Type{
			scanner.NewNamed("foo", "Foo"),
			scanner.NewBasic("bool"),
			scanner.NewNamed("", "error"),
		},
	}
	pkg := &Package{Path: "baz"}
	rpc := s.t.transformFunc(pkg, fn, nameSet{})

	s.NotNil(rpc)
	s.Equal(fn.Name, rpc.Name)
	s.Equal(NewGeneratedNamed("baz", "DoFooRequest"), rpc.Input)
	s.Equal(NewGeneratedNamed("baz", "DoFooResponse"), rpc.Output)

	s.Equal(2, len(pkg.Messages), "two messages should have been created")
	msg := pkg.Messages[0]
	s.Equal("DoFooRequest", msg.Name)
	s.Equal(2, len(msg.Fields), "DoFooRequest should have same fields as args")
	s.assertField(msg.Fields[0], "arg1", NewNamed("foo", "Bar"))
	s.assertField(msg.Fields[1], "arg2", NewBasic("int32"))

	msg = pkg.Messages[1]
	s.Equal("DoFooResponse", msg.Name)
	s.Equal(2, len(msg.Fields), "DoFooResponse should have same results as return args")
	s.assertField(msg.Fields[0], "result1", NewNamed("foo", "Foo"))
	s.assertField(msg.Fields[1], "result2", NewBasic("bool"))
}

func (s *TransformerSuite) TestTransformFuncInputRegistered() {
	fn := &scanner.Func{
		Name: "DoFoo",
		Input: []scanner.Type{
			scanner.NewNamed("foo", "Bar"),
			scanner.NewBasic("int"),
		},
		Output: []scanner.Type{
			scanner.NewNamed("foo", "Foo"),
			scanner.NewBasic("bool"),
			scanner.NewNamed("", "error"),
		},
	}
	rpc := s.t.transformFunc(&Package{}, fn, nameSet{"DoFooRequest": struct{}{}})

	s.Nil(rpc)
}

func (s *TransformerSuite) TestTransformFuncOutputRegistered() {
	fn := &scanner.Func{
		Name: "DoFoo",
		Input: []scanner.Type{
			scanner.NewNamed("foo", "Bar"),
			scanner.NewBasic("int"),
		},
		Output: []scanner.Type{
			scanner.NewNamed("foo", "Foo"),
			scanner.NewBasic("bool"),
			scanner.NewNamed("", "error"),
		},
	}
	rpc := s.t.transformFunc(&Package{}, fn, nameSet{"DoFooResponse": struct{}{}})

	s.Nil(rpc)
}

func (s *TransformerSuite) TestTransformFuncEmpty() {
	fn := &scanner.Func{Name: "DoFoo"}
	pkg := &Package{Path: "baz"}
	rpc := s.t.transformFunc(pkg, fn, nameSet{})

	s.NotNil(rpc)
	s.Equal(fn.Name, rpc.Name)
	s.Equal(NewGeneratedNamed("baz", "DoFooRequest"), rpc.Input)
	s.Equal(NewGeneratedNamed("baz", "DoFooResponse"), rpc.Output)
	s.Equal(2, len(pkg.Messages), "two messages should have been created")
	msg := pkg.Messages[0]
	s.Equal("DoFooRequest", msg.Name)
	s.Equal(0, len(msg.Fields), "DoFooRequest should have no args")

	msg = pkg.Messages[1]
	s.Equal("DoFooResponse", msg.Name)
	s.Equal(0, len(msg.Fields), "DoFooResponse should have no results")
}

func (s *TransformerSuite) TestTransformFunc1BasicArg() {
	fn := &scanner.Func{
		Name: "DoFoo",
		Input: []scanner.Type{
			scanner.NewBasic("int"),
		},
		Output: []scanner.Type{
			scanner.NewBasic("bool"),
			scanner.NewNamed("", "error"),
		},
	}
	pkg := new(Package)
	rpc := s.t.transformFunc(pkg, fn, nameSet{})

	s.NotNil(rpc)
	s.Equal(fn.Name, rpc.Name)
	s.Equal(NewGeneratedNamed("", "DoFooRequest"), rpc.Input)
	s.Equal(NewGeneratedNamed("", "DoFooResponse"), rpc.Output)

	s.Equal(2, len(pkg.Messages), "two messages should have been created")
	msg := pkg.Messages[0]
	s.Equal("DoFooRequest", msg.Name)
	s.Equal(1, len(msg.Fields), "DoFooRequest should have same fields as args")
	s.assertField(msg.Fields[0], "arg1", NewBasic("int32"))

	msg = pkg.Messages[1]
	s.Equal("DoFooResponse", msg.Name)
	s.Equal(1, len(msg.Fields), "DoFooResponse should have same results as return args")
	s.assertField(msg.Fields[0], "result1", NewBasic("bool"))
}

func (s *TransformerSuite) TestTransformFunc1NamedArg() {
	fn := &scanner.Func{
		Name: "DoFoo",
		Input: []scanner.Type{
			scanner.NewNamed("foo", "Foo"),
		},
		Output: []scanner.Type{
			scanner.NewNamed("foo", "Bar"),
			scanner.NewNamed("", "error"),
		},
	}
	rpc := s.t.transformFunc(new(Package), fn, nameSet{})

	s.NotNil(rpc)
	s.Equal(fn.Name, rpc.Name)
	s.Equal(NewNamed("foo", "Foo"), rpc.Input)
	s.Equal(NewNamed("foo", "Bar"), rpc.Output)
}

func (s *TransformerSuite) TestTransformFuncReceiver() {
	fn := &scanner.Func{
		Name:     "DoFoo",
		Receiver: scanner.NewNamed("foo", "Fooer"),
	}
	rpc := s.t.transformFunc(new(Package), fn, nameSet{})
	s.NotNil(rpc)
	s.Equal("Fooer_DoFoo", rpc.Name)
}

func (s *TransformerSuite) TestTransformFuncReceiverInvalid() {
	fn := &scanner.Func{
		Name:     "DoFoo",
		Receiver: scanner.NewBasic("int"),
	}
	rpc := s.t.transformFunc(new(Package), fn, nameSet{})
	s.Nil(rpc)
}

func (s *TransformerSuite) TestTransformFuncRepeatedSingle() {
	fn := &scanner.Func{
		Name:       "DoFoo",
		IsVariadic: true,
		Input: []scanner.Type{
			repeated(scanner.NewBasic("int")),
		},
		Output: []scanner.Type{
			repeated(scanner.NewBasic("bool")),
			scanner.NewNamed("", "error"),
		},
	}
	pkg := new(Package)
	rpc := s.t.transformFunc(pkg, fn, nameSet{})

	s.NotNil(rpc)
	s.Equal(fn.Name, rpc.Name)
	s.Equal(rpc.Input, NewGeneratedNamed("", "DoFooRequest"))
	s.Equal(rpc.Output, NewGeneratedNamed("", "DoFooResponse"))

	s.Equal(2, len(pkg.Messages), "two messages should have been created")
	msg := pkg.Messages[0]
	s.Equal("DoFooRequest", msg.Name)
	s.Equal(1, len(msg.Fields), "DoFooRequest should have same fields as args")
	s.assertField(msg.Fields[0], "arg1", NewBasic("int32"))
	s.True(msg.Fields[0].Repeated, "field should be repeated")

	msg = pkg.Messages[1]
	s.Equal("DoFooResponse", msg.Name)
	s.Equal(1, len(msg.Fields), "DoFooResponse should have same results as return args")
	s.assertField(msg.Fields[0], "result1", NewBasic("bool"))
	s.True(msg.Fields[0].Repeated, "field should be repeated")
}

func (s *TransformerSuite) TestTransformEnum() {
	enum := s.t.transformEnum(&scanner.Enum{
		Name:   "Foo",
		Values: []string{"Foo", "Bar", "BarBaz"},
	})

	s.Equal("Foo", enum.Name)
	s.Equal(3, len(enum.Values), "should have same number of values")
	s.assertEnumVal(enum.Values[0], "FOO", 0)
	s.assertEnumVal(enum.Values[1], "BAR", 1)
	s.assertEnumVal(enum.Values[2], "BAR_BAZ", 2)
	s.Equal(NewLiteralValue("true"), enum.Options["(gogoproto.enum_drop_type_declaration)"], "should drop declaration by default")
}

func (s *TransformerSuite) TestTransform() {
	pkgs := s.fixtures()
	pkg := s.t.Transform(pkgs[0])

	s.Equal("github.com.srcd.proteus.fixtures", pkg.Name)
	s.Equal("github.com/src-d/proteus/fixtures", pkg.Path)
	s.Equal(NewStringValue("foo"), pkg.Options["go_package"])
	s.Equal([]string{
		"github.com/src-d/protobuf/gogoproto/gogo.proto",
		"google/protobuf/timestamp.proto",
		"github.com/src-d/proteus/fixtures/subpkg/generated.proto",
	}, pkg.Imports)
	s.Equal(1, len(pkg.Enums))
	s.Equal(4, len(pkg.Messages))
	s.Equal(0, len(pkg.RPCs))

	pkg = s.t.Transform(pkgs[1])
	s.Equal("github.com.srcd.proteus.fixtures.subpkg", pkg.Name)
	s.Equal("github.com/src-d/proteus/fixtures/subpkg", pkg.Path)
	s.Equal(NewStringValue("subpkg"), pkg.Options["go_package"])
	s.Equal([]string{"github.com/src-d/protobuf/gogoproto/gogo.proto"}, pkg.Imports)
	s.Equal(0, len(pkg.Enums))

	var msgs = []string{
		"GeneratedRequest",
		"GeneratedResponse",
		"MyContainer_NameRequest",
		"MyContainer_NameResponse",
		"Point",
		"Point_GeneratedMethodOnPointerRequest",
		"Point_GeneratedMethodRequest",
	}
	s.Equal(len(msgs), len(pkg.Messages))
	for _, m := range pkg.Messages {
		s.True(hasString(m.Name, msgs), fmt.Sprintf("should have message %s", m.Name))
	}

	s.Equal(4, len(pkg.RPCs))
}

func hasString(str string, coll []string) bool {
	for _, s := range coll {
		if s == str {
			return true
		}
	}
	return false
}

func (s *TransformerSuite) fixtures() []*scanner.Package {
	sc, err := scanner.New(projectPath("fixtures"), projectPath("fixtures/subpkg"))
	s.Nil(err)
	pkgs, err := sc.Scan()
	s.Nil(err)
	resolver.New().Resolve(pkgs)
	return pkgs
}

func (s *TransformerSuite) assertField(f *Field, name string, typ Type) {
	s.Equal(f.Name, name)
	s.Equal(f.Type, typ)
}

func (s *TransformerSuite) assertEnumVal(v *EnumValue, name string, val uint) {
	s.Equal(name, v.Name)
	s.Equal(val, v.Value)
}

func TestTransformer(t *testing.T) {
	suite.Run(t, new(TransformerSuite))
}

func repeated(t scanner.Type) scanner.Type {
	t.SetRepeated(true)
	return t
}

const project = "github.com/src-d/proteus"

func projectPath(pkg string) string {
	return filepath.Join(project, pkg)
}
