package halide

// TypeCode is the Halide element category stored in halide_type_t.
type TypeCode uint8

const (
	TypeInt TypeCode = iota
	TypeUInt
	TypeFloat
	TypeHandle
	TypeBFloat
)

// Type is one Halide element type. Lanes is 1 for scalars.
type Type struct {
	Code  TypeCode
	Bits  uint8
	Lanes uint16
}

// Bytes is the size of one element, lanes included only as a single vector
// width: Halide stores lanes in the type and the element stride is still
// Bits/8 for the dense host layout used by this package (lanes == 1).
func (t Type) Bytes() int {
	if t.Bits == 0 {
		return 0
	}
	return int(t.Bits+7) / 8
}

// Equal reports whether the two types describe the same elements.
func (t Type) Equal(o Type) bool {
	return t.Code == o.Code && t.Bits == o.Bits && t.Lanes == o.Lanes
}

func (t Type) String() string {
	name := "invalid"
	switch t.Code {
	case TypeInt:
		name = "int"
	case TypeUInt:
		name = "uint"
	case TypeFloat:
		name = "float"
	case TypeHandle:
		name = "handle"
	case TypeBFloat:
		name = "bfloat"
	}
	if t.Lanes > 1 {
		return name + uitoa(uint64(t.Bits)) + "x" + uitoa(uint64(t.Lanes))
	}
	return name + uitoa(uint64(t.Bits))
}

// Scalar types used by buffers and by generated wrappers.
var (
	Int8    = Type{Code: TypeInt, Bits: 8, Lanes: 1}
	Int16   = Type{Code: TypeInt, Bits: 16, Lanes: 1}
	Int32   = Type{Code: TypeInt, Bits: 32, Lanes: 1}
	Int64   = Type{Code: TypeInt, Bits: 64, Lanes: 1}
	UInt8   = Type{Code: TypeUInt, Bits: 8, Lanes: 1}
	UInt16  = Type{Code: TypeUInt, Bits: 16, Lanes: 1}
	UInt32  = Type{Code: TypeUInt, Bits: 32, Lanes: 1}
	UInt64  = Type{Code: TypeUInt, Bits: 64, Lanes: 1}
	Float32 = Type{Code: TypeFloat, Bits: 32, Lanes: 1}
	Float64 = Type{Code: TypeFloat, Bits: 64, Lanes: 1}
	Bool    = Type{Code: TypeUInt, Bits: 1, Lanes: 1}
)

// Kind is the role of one filter argument, matching halide_argument_kind_t.
type Kind int32

const (
	KindInputScalar Kind = iota
	KindInputBuffer
	KindOutputBuffer
)

func (k Kind) String() string {
	switch k {
	case KindInputScalar:
		return "input scalar"
	case KindInputBuffer:
		return "input buffer"
	case KindOutputBuffer:
		return "output buffer"
	default:
		return "argument"
	}
}

// Arg is one entry from a filter's Halide metadata.
type Arg struct {
	Name       string
	Kind       Kind
	Dimensions int
	Type       Type
}

func uitoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
