package halide

import "fmt"

// PipelineError is a non-zero status returned by a Halide filter.
// Text is the message passed to the runtime error handler when one was captured.
type PipelineError struct {
	Func string
	Code int32
	Text string
}

func (e *PipelineError) Error() string {
	name := errorName(e.Code)
	if e.Text != "" {
		return fmt.Sprintf("halide: %s: %s (%d): %s", e.Func, name, e.Code, e.Text)
	}
	return fmt.Sprintf("halide: %s: %s (%d)", e.Func, name, e.Code)
}

func errorName(code int32) string {
	if code == 0 {
		return "success"
	}
	if name, ok := errorNames[code]; ok {
		return name
	}
	return "halide error"
}

// Codes from HalideRuntime.h (Halide 21).
var errorNames = map[int32]string{
	-1:  "generic_error",
	-2:  "explicit_bounds_too_small",
	-3:  "bad_type",
	-4:  "access_out_of_bounds",
	-5:  "buffer_allocation_too_large",
	-6:  "buffer_extents_too_large",
	-7:  "constraints_make_required_region_smaller",
	-8:  "constraint_violated",
	-9:  "param_too_small",
	-10: "param_too_large",
	-11: "out_of_memory",
	-12: "buffer_argument_is_null",
	-13: "debug_to_file_failed",
	-14: "copy_to_host_failed",
	-15: "copy_to_device_failed",
	-16: "device_malloc_failed",
	-17: "device_sync_failed",
	-18: "device_free_failed",
	-19: "no_device_interface",
	-20: "unimplemented",
	-21: "symbol_not_found",
	-22: "internal_error",
	-23: "device_run_failed",
	-24: "unaligned_host_ptr",
	-25: "bad_fold",
	-26: "fold_factor_too_small",
	-27: "requirement_failed",
	-28: "buffer_extents_negative",
	-29: "gpu_device_error",
	-30: "trace_failed",
	-31: "specialize_fail",
	-32: "device_wrap_native_failed",
	-33: "device_detach_native_failed",
	-34: "host_is_null",
	-35: "bad_extern_fold",
	-36: "device_interface_no_device",
	-37: "host_and_device_dirty",
	-38: "buffer_is_null",
	-39: "device_buffer_copy_failed",
	-40: "device_crop_unsupported",
	-41: "device_crop_failed",
	-42: "incompatible_device_interface",
	-43: "bad_dimensions",
	-44: "device_dirty_with_no_device_support",
	-45: "storage_bound_too_small",
	-46: "split_factor_not_positive",
	-47: "vscale_invalid",
	-48: "cannot_profile_pipeline",
}
