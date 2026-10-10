#include "Halide.h"

using namespace Halide;

namespace {

template <typename FuncT>
void schedule_pointwise(FuncT &f, Var x, Var y, const Target &t, int vx) {
    if (t.arch == Target::WebAssembly) {
        f.vectorize(x, vx, TailStrategy::GuardWithIf);
    } else {
        f.vectorize(x, vx, TailStrategy::GuardWithIf).parallel(y);
    }
}

}  // namespace

// 3x3 box blur, repeat-edge, integer division by 9.
class Blur : public Generator<Blur> {
 public:
    Input<Buffer<uint8_t, 2>> in{"in"};
    Output<Buffer<uint8_t, 2>> out{"out"};

    void generate() {
        Var x("x"), y("y"), yi("yi");
        Func clamped = BoundaryConditions::repeat_edge(in);
        Func bx("bx");
        bx(x, y) = cast<uint16_t>(clamped(x - 1, y)) + cast<uint16_t>(clamped(x, y)) +
                   cast<uint16_t>(clamped(x + 1, y));
        out(x, y) = cast<uint8_t>((bx(x, y - 1) + bx(x, y) + bx(x, y + 1)) / 9);

        const int vx = natural_vector_size<uint16_t>();
        if (get_target().arch == Target::WebAssembly) {
            out.vectorize(x, vx, TailStrategy::GuardWithIf);
            bx.compute_at(out, y).vectorize(x, vx, TailStrategy::GuardWithIf);
        } else {
            out.split(y, y, yi, 32, TailStrategy::GuardWithIf)
                .parallel(y)
                .vectorize(x, vx, TailStrategy::GuardWithIf);
            bx.compute_at(out, yi).store_at(out, y).vectorize(x, vx, TailStrategy::GuardWithIf);
        }
    }
};

// brightness is added after contrast.
// contrast is 8.8 fixed point: 256 means 1.0.
// v = clamp(((p - 128) * contrast) / 256 + 128 + brightness, 0, 255)
class Brightness : public Generator<Brightness> {
 public:
    Input<Buffer<uint8_t, 2>> in{"in"};
    Input<int32_t> brightness{"brightness"};
    Input<int32_t> contrast{"contrast"};
    Output<Buffer<uint8_t, 2>> out{"out"};

    void generate() {
        Var x("x"), y("y");
        Expr centered = cast<int32_t>(in(x, y)) - 128;
        // Halide's / rounds toward -inf. This matches Go's toward-zero division.
        Expr scaled = div_round_to_zero(centered * contrast, 256);
        Expr v = scaled + 128 + brightness;
        out(x, y) = cast<uint8_t>(clamp(v, 0, 255));
        schedule_pointwise(out, x, y, get_target(), natural_vector_size<uint8_t>());
    }
};

// in is interleaved (channel, x, y). Channels 0, 1, 2 are R, G, B.
// Y = (77*R + 150*G + 29*B) >> 8. 77+150+29 = 256, so white stays white.
class Grayscale : public Generator<Grayscale> {
 public:
    Input<Buffer<uint8_t, 3>> in{"in"};
    Output<Buffer<uint8_t, 2>> out{"out"};

    void generate() {
        Var x("x"), y("y");
        Expr r = cast<uint16_t>(in(0, x, y));
        Expr g = cast<uint16_t>(in(1, x, y));
        Expr b = cast<uint16_t>(in(2, x, y));
        out(x, y) = cast<uint8_t>((77 * r + 150 * g + 29 * b) >> 8);
        schedule_pointwise(out, x, y, get_target(), natural_vector_size<uint8_t>());
    }
};

// Sobel magnitude with repeat-edge. |gx| + |gy|, saturated to 255.
class Sobel : public Generator<Sobel> {
 public:
    Input<Buffer<uint8_t, 2>> in{"in"};
    Output<Buffer<uint8_t, 2>> out{"out"};

    void generate() {
        Var x("x"), y("y");
        Func c = BoundaryConditions::repeat_edge(in);
        Expr gx = -cast<int16_t>(c(x - 1, y - 1)) + cast<int16_t>(c(x + 1, y - 1)) -
                  2 * cast<int16_t>(c(x - 1, y)) + 2 * cast<int16_t>(c(x + 1, y)) -
                  cast<int16_t>(c(x - 1, y + 1)) + cast<int16_t>(c(x + 1, y + 1));
        Expr gy = -cast<int16_t>(c(x - 1, y - 1)) - 2 * cast<int16_t>(c(x, y - 1)) -
                  cast<int16_t>(c(x + 1, y - 1)) + cast<int16_t>(c(x - 1, y + 1)) +
                  2 * cast<int16_t>(c(x, y + 1)) + cast<int16_t>(c(x + 1, y + 1));
        Expr mag = abs(gx) + abs(gy);
        out(x, y) = cast<uint8_t>(min(mag, cast<int16_t>(255)));
        schedule_pointwise(out, x, y, get_target(), natural_vector_size<uint8_t>());
    }
};

// Nearest-neighbor resize. The output buffer's extent is the destination size.
class Resize : public Generator<Resize> {
 public:
    Input<Buffer<uint8_t, 2>> in{"in"};
    Output<Buffer<uint8_t, 2>> out{"out"};

    void generate() {
        Var x("x"), y("y");
        Expr sx = (x * in.width()) / out.width();
        Expr sy = (y * in.height()) / out.height();
        out(x, y) = in(sx, sy);
        schedule_pointwise(out, x, y, get_target(), natural_vector_size<uint8_t>());
    }
};

HALIDE_REGISTER_GENERATOR(Blur, blur)
HALIDE_REGISTER_GENERATOR(Brightness, brightness)
HALIDE_REGISTER_GENERATOR(Grayscale, grayscale)
HALIDE_REGISTER_GENERATOR(Sobel, sobel)
HALIDE_REGISTER_GENERATOR(Resize, resize)
