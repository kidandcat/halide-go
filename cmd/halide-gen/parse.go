package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// filter describes one generated Halide function.
type filter struct {
	CName string
	Args  []argInfo
}

type argInfo struct {
	Name       string
	Kind       string // InputBuffer, OutputBuffer, InputScalar
	Dimensions int
	Code       string // int, uint, float, handle, bfloat
	Bits       int
	Lanes      int
}

func (a argInfo) goType() (string, error) {
	switch a.Kind {
	case "InputBuffer", "OutputBuffer":
		return "*halide.Buffer", nil
	case "InputScalar":
		return scalarGoType(a.Code, a.Bits)
	default:
		return "", fmt.Errorf("unknown argument kind %s", a.Kind)
	}
}

func scalarGoType(code string, bits int) (string, error) {
	switch code {
	case "int":
		switch bits {
		case 8, 16, 32, 64:
			return fmt.Sprintf("int%d", bits), nil
		}
	case "uint":
		if bits == 1 {
			return "bool", nil
		}
		switch bits {
		case 8, 16, 32, 64:
			return fmt.Sprintf("uint%d", bits), nil
		}
	case "float":
		switch bits {
		case 32:
			return "float32", nil
		case 64:
			return "float64", nil
		}
	}
	return "", fmt.Errorf("unsupported scalar %s%d", code, bits)
}

var (
	fnInfoRe = regexp.MustCompile(`(?s)inline constexpr std::array<[^>]+>\s+([A-Za-z_][A-Za-z0-9_]*)_argument_info\(\)`)
	argRe    = regexp.MustCompile(`\{\s*"([^"]+)"\s*,\s*::HalideFunctionInfo::(InputBuffer|OutputBuffer|InputScalar)\s*,\s*(-?\d+)\s*,\s*halide_type_t\s*\{\s*halide_type_(int|uint|float|handle|bfloat)\s*,\s*(\d+)\s*,\s*(\d+)\s*\}\s*\}`)
	sigRe    = regexp.MustCompile(`(?m)^[ \t]*int[ \t]+([A-Za-z_][A-Za-z0-9_]*)[ \t]*\(([^;]*)\)[ \t]*;`)
	regRe    = regexp.MustCompile(`HALIDE_REGISTER_GENERATOR\s*\(\s*[A-Za-z_][A-Za-z0-9_]*\s*,\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)`)
	pyNameRe = regexp.MustCompile(`@hl\.generator\(\s*(?:name\s*=\s*)?["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\)`)
)

func parseFunctionInfo(src, want string) (filter, error) {
	loc := fnInfoRe.FindStringIndex(src)
	if loc == nil {
		return filter{}, fmt.Errorf("no argument_info function in header")
	}
	// Take the body that belongs to this function, up to the closing of the array.
	rest := src[loc[0]:]
	end := strings.Index(rest, "};")
	if end < 0 {
		return filter{}, fmt.Errorf("unterminated argument_info")
	}
	body := rest[:end]
	name := fnInfoRe.FindStringSubmatch(rest[:end+2])
	if name == nil {
		return filter{}, fmt.Errorf("could not read argument_info name")
	}
	if want != "" && name[1] != want {
		// A header can only describe one filter; the caller passes the expected name.
		return filter{}, fmt.Errorf("header describes %s, want %s", name[1], want)
	}
	matches := argRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return filter{}, fmt.Errorf("no arguments in %s_argument_info", name[1])
	}
	f := filter{CName: name[1]}
	for _, m := range matches {
		dim, err := strconv.Atoi(m[3])
		if err != nil {
			return filter{}, err
		}
		bits, err := strconv.Atoi(m[5])
		if err != nil {
			return filter{}, err
		}
		lanes, err := strconv.Atoi(m[6])
		if err != nil {
			return filter{}, err
		}
		f.Args = append(f.Args, argInfo{
			Name: m[1], Kind: m[2], Dimensions: dim, Code: m[4], Bits: bits, Lanes: lanes,
		})
	}
	return f, nil
}

func parseCSignature(header, name string) ([]string, error) {
	for _, m := range sigRe.FindAllStringSubmatch(header, -1) {
		if m[1] == name {
			return splitParams(m[2]), nil
		}
	}
	return nil, fmt.Errorf("C signature for %s not found", name)
}

func splitParams(list string) []string {
	list = strings.TrimSpace(list)
	if list == "" || list == "void" {
		return nil
	}
	parts := strings.Split(list, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func registeredGenerators(src string) []string {
	var names []string
	seen := map[string]bool{}
	for _, m := range regRe.FindAllStringSubmatch(src, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	return names
}

func pythonGeneratorNames(src string) []string {
	var names []string
	seen := map[string]bool{}
	for _, m := range pyNameRe.FindAllStringSubmatch(src, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	return names
}
