package onb

import "fmt"

// EmitObject emits a relocatable object for the current phase 1.0 ONB slice.
func EmitObject(program *Program) ([]byte, error) {
	if program == nil {
		return nil, fmt.Errorf("onb: missing program")
	}
	switch program.Target.ObjectFormat {
	case "mach-o":
		return emitMachOObject(program)
	default:
		return nil, fmt.Errorf("%w: %s object writer", ErrNotImplemented, program.Target.ObjectFormat)
	}
}
