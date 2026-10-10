package main

import (
	"strings"
	"testing"
)

const sampleInfo = `
inline constexpr std::array<::HalideFunctionInfo::ArgumentInfo, 4> brightness_argument_info() {
 return {{
  {"in", ::HalideFunctionInfo::InputBuffer, 2, halide_type_t{halide_type_uint, 8, 1}},
  {"brightness", ::HalideFunctionInfo::InputScalar, 0, halide_type_t{halide_type_int, 32, 1}},
  {"contrast", ::HalideFunctionInfo::InputScalar, 0, halide_type_t{halide_type_int, 32, 1}},
  {"out", ::HalideFunctionInfo::OutputBuffer, 2, halide_type_t{halide_type_uint, 8, 1}},
 }};
}
`

const sampleHeader = `
int brightness(struct halide_buffer_t *_in_buffer, int32_t _brightness, int32_t _contrast, struct halide_buffer_t *_out_buffer);
int brightness_argv(void **args);
const struct halide_filter_metadata_t *brightness_metadata();
`

func TestParseFunctionInfo(t *testing.T) {
	f, err := parseFunctionInfo(sampleInfo, "brightness")
	if err != nil {
		t.Fatal(err)
	}
	if f.CName != "brightness" || len(f.Args) != 4 {
		t.Fatalf("%+v", f)
	}
	if f.Args[1].Kind != "InputScalar" || f.Args[1].Bits != 32 || f.Args[1].Code != "int" {
		t.Fatalf("scalar %+v", f.Args[1])
	}
	gt, err := f.Args[0].goType()
	if err != nil || gt != "*halide.Buffer" {
		t.Fatal(gt, err)
	}
	gt, err = f.Args[1].goType()
	if err != nil || gt != "int32" {
		t.Fatal(gt, err)
	}
	params, err := parseCSignature(sampleHeader, "brightness")
	if err != nil {
		t.Fatal(err)
	}
	if len(params) != len(f.Args) {
		t.Fatalf("signature arity %d", len(params))
	}
}

func TestRegisteredNames(t *testing.T) {
	src := `
HALIDE_REGISTER_GENERATOR(Blur, blur)
HALIDE_REGISTER_GENERATOR(Brightness, brightness)
`
	got := registeredGenerators(src)
	if strings.Join(got, ",") != "blur,brightness" {
		t.Fatal(got)
	}
	py := pythonGeneratorNames("@hl.generator(name=\"blur\")\n@hl.generator(\"sobel\")\n")
	if strings.Join(py, ",") != "blur,sobel" {
		t.Fatal(py)
	}
}

func TestEmitGo(t *testing.T) {
	f, err := parseFunctionInfo(sampleInfo, "brightness")
	if err != nil {
		t.Fatal(err)
	}
	src, err := emitGo("filters", "filters", []filter{f})
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, want := range []string{
		"func (p *Pipeline) Brightness(in *halide.Buffer, brightness int32, contrast int32, out *halide.Buffer) error",
		"func Brightness(",
		`halide.OpenEmbedded(embedded, "filters"`,
		"//go:embed libs",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s\n%s", want, text)
		}
	}
}

func TestBoolScalar(t *testing.T) {
	got, err := scalarGoType("uint", 1)
	if err != nil || got != "bool" {
		t.Fatal(got, err)
	}
}
