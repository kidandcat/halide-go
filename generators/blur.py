"""Python Halide generator equivalent of the C++ blur pipeline.

Run it through halide-gen (the file calls hl.main when executed directly):

    go run github.com/kidandcat/halide-go/cmd/halide-gen -o ./out generators/blur.py
"""

import halide as hl


@hl.generator(name="blur")
class Blur:
    src = hl.InputBuffer(hl.UInt(8), 2)
    out = hl.OutputBuffer(hl.UInt(8), 2)

    def generate(g):
        x, y = hl.Var("x"), hl.Var("y")
        c = hl.BoundaryConditions.repeat_edge(g.src)
        bx = hl.Func("bx")
        bx[x, y] = hl.u16(c[x - 1, y]) + hl.u16(c[x, y]) + hl.u16(c[x + 1, y])
        g.out[x, y] = hl.u8((bx[x, y - 1] + bx[x, y] + bx[x, y + 1]) / 9)
        vx = g.natural_vector_size(hl.UInt(16))
        tail = hl.TailStrategy.GuardWithIf
        if g.target().arch == hl.TargetArch.WebAssembly:
            g.out.vectorize(x, vx, tail)
            bx.compute_at(g.out, y).vectorize(x, vx, tail)
        else:
            yi = hl.Var("yi")
            g.out.split(y, y, yi, 32, tail).parallel(y).vectorize(x, vx, tail)
            bx.compute_at(g.out, yi).store_at(g.out, y).vectorize(x, vx, tail)


if __name__ == "__main__":
    hl.main()
