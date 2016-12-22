package subpkg

// Point ...
//proteus:generate
type Point struct {
	X int
	Y int
}

func (p *Point) Dist(p2 Point) float64 {
	return .0
}

// NotGenerated ...
type NotGenerated struct{}

func Foo(a int) (float64, error) {
	return float64(a), nil
}
