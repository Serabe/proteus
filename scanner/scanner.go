package scanner

import (
	"errors"
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/src-d/proteus/report"
)

// Scanner scans packages looking for Go source files to parse
// and extract types and structs from.
type Scanner struct {
	packages []string
	importer *Importer
}

// ErrNoGoPathSet is the error returned when the GOPATH variable is not
// set.
var ErrNoGoPathSet = errors.New("GOPATH environment variable is not set")

// New creates a new Scanner that will look for types and structs
// only in the given packages.
func New(packages ...string) (*Scanner, error) {
	if goPath == "" {
		return nil, ErrNoGoPathSet
	}

	for _, pkg := range packages {
		p := filepath.Join(goPath, "src", pkg)
		fi, err := os.Stat(p)
		switch {
		case err != nil:
			return nil, err
		case !fi.IsDir():
			return nil, fmt.Errorf("path is not directory: %s", p)
		}
	}

	return &Scanner{
		packages: packages,
		importer: NewImporter(),
	}, nil
}

// Scan retrieves the scanned packages containing the extracted
// go types and structs.
func (s *Scanner) Scan() ([]*Package, error) {
	var (
		pkgs   = make([]*Package, len(s.packages))
		errors errorList
		mut    sync.Mutex
		wg     = new(sync.WaitGroup)
	)

	wg.Add(len(s.packages))
	for i, p := range s.packages {
		go func(p string, i int) {
			defer wg.Done()

			pkg, err := s.scanPackage(p)
			mut.Lock()
			defer mut.Unlock()
			if err != nil {
				errors.add(fmt.Errorf("error scanning package %q: %s", p, err))
				return
			}

			pkgs[i] = pkg
		}(p, i)
	}

	wg.Wait()
	if len(errors) > 0 {
		return nil, errors.err()
	}

	return pkgs, nil
}

func (s *Scanner) scanPackage(p string) (*Package, error) {
	pkg, err := s.importer.Import(p)
	if err != nil {
		return nil, err
	}

	ctx, err := newContext(p)
	if err != nil {
		return nil, err
	}

	return buildPackage(ctx, pkg)
}

func buildPackage(ctx *context, gopkg *types.Package) (*Package, error) {
	objs := objectsInScope(gopkg.Scope())

	pkg := &Package{
		Path:    removeGoPath(gopkg.Path()),
		Name:    gopkg.Name(),
		Aliases: make(map[string]Type),
	}

	for _, o := range objs {
		pkg.scanObject(ctx, o)
	}

	pkg.collectEnums(ctx)
	return pkg, nil
}

func (p *Package) scanObject(ctx *context, o types.Object) {
	if !o.Exported() {
		return
	}

	switch t := o.Type().(type) {
	case *types.Named:
		switch o.(type) {
		case *types.Const:
			if _, ok := t.Underlying().(*types.Basic); ok {
				scanEnumValue(ctx, o.Name(), t)
			}
		case *types.TypeName:
			if s, ok := t.Underlying().(*types.Struct); ok {
				st := scanStruct(&Struct{
					Name:     o.Name(),
					Generate: ctx.shouldGenerateType(o.Name()),
				}, s)
				p.Structs = append(p.Structs, st)
				return
			}

			p.Aliases[objName(t.Obj())] = scanType(t.Underlying())
		}
	case *types.Signature:
		// TODO: find by qualified name
		t.Recv().Type().Underlying().(*types.Named).Obj().Name()
		if ctx.shouldGenerateFunc(o.Name()) {
			fn := scanFunc(&Func{Name: o.Name()}, t)
			p.Funcs = append(p.Funcs, fn)
		}
	}
}

func scanType(typ types.Type) (t Type) {
	switch u := typ.(type) {
	case *types.Named:
		t = NewNamed(
			removeGoPath(u.Obj().Pkg().Path()),
			u.Obj().Name(),
		)
	case *types.Basic:
		t = NewBasic(u.Name())
	case *types.Slice:
		t = scanType(u.Elem())
		t.SetRepeated(true)
	case *types.Array:
		t = scanType(u.Elem())
		t.SetRepeated(true)
	case *types.Pointer:
		t = scanType(u.Elem())
	case *types.Map:
		key := scanType(u.Key())
		val := scanType(u.Elem())
		t = NewMap(key, val)
	default:
		report.Warn("ignoring type %s", typ.String())
		return nil
	}

	return
}

func scanEnumValue(ctx *context, name string, named *types.Named) {
	typ := objName(named.Obj())
	ctx.enumValues[typ] = append(ctx.enumValues[typ], name)
}

func scanStruct(s *Struct, elem *types.Struct) *Struct {
	for i := 0; i < elem.NumFields(); i++ {
		v := elem.Field(i)
		tags := findProtoTags(elem.Tag(i))

		if isIgnoredField(v, tags) {
			continue
		}

		// TODO: It has not been decided yet what exact behaviour
		// is the intended when a struct overrides a field from
		// a previously embedded type. For now, the field is just
		// completely ignored and a warning is printed to give
		// feedback to the user.
		if s.HasField(v.Name()) {
			report.Warn("struct %q already has a field %q", s.Name, v.Name())
			continue
		}

		if v.Anonymous() {
			embedded := findStruct(v.Type())
			if embedded == nil {
				report.Warn("field %q with type %q is not a valid embedded type", v.Name(), v.Type())
			} else {
				s = scanStruct(s, embedded)
			}
			continue
		}

		f := &Field{
			Name: v.Name(),
			Type: scanType(v.Type()),
		}
		if f.Type == nil {
			continue
		}

		s.Fields = append(s.Fields, f)
	}

	return s
}

func scanFunc(fn *Func, signature *types.Signature) *Func {
	// TODO: impl
	return fn
}

func findStruct(t types.Type) *types.Struct {
	switch elem := t.(type) {
	case *types.Pointer:
		return findStruct(elem.Elem())
	case *types.Named:
		return findStruct(elem.Underlying())
	case *types.Struct:
		return elem
	default:
		return nil
	}
}

// newEnum creates a new enum with the given name.
// The values are looked up in the ast package and only if they are constants
// they will be added as enum values.
// All values are guaranteed to be sorted by their iota.
func newEnum(ctx *context, name string, vals []string) *Enum {
	enum := &Enum{Name: name}
	var values enumValues
	for _, v := range vals {
		if obj, ok := ctx.consts[v]; ok {
			values = append(values, enumValue{
				name: v,
				pos:  uint(obj.Data.(int)),
			})
		}
	}

	sort.Stable(values)

	for _, v := range values {
		enum.Values = append(enum.Values, v.name)
	}

	return enum
}

type enumValue struct {
	name string
	pos  uint
}

type enumValues []enumValue

func (v enumValues) Swap(i, j int) {
	v[j], v[i] = v[i], v[j]
}

func (v enumValues) Len() int {
	return len(v)
}

func (v enumValues) Less(i, j int) bool {
	return v[i].pos < v[j].pos
}

func isIgnoredField(f *types.Var, tags []string) bool {
	return !f.Exported() || (len(tags) > 0 && tags[0] == "-")
}

func objectsInScope(scope *types.Scope) (objs []types.Object) {
	for _, n := range scope.Names() {
		objs = append(objs, scope.Lookup(n))
	}
	return
}

func objName(obj types.Object) string {
	return fmt.Sprintf("%s.%s", removeGoPath(obj.Pkg().Path()), obj.Name())
}

func removeGoPath(path string) string {
	return strings.Replace(path, filepath.Join(goPath, "src")+"/", "", -1)
}

type errorList []error

func (l *errorList) add(err error) {
	*l = append(*l, err)
}

func (l errorList) err() error {
	var lines []string
	for _, err := range l {
		lines = append(lines, err.Error())
	}
	return errors.New(strings.Join(lines, "\n"))
}
