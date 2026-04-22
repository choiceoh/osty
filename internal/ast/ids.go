package ast

import "reflect"

// AssignIDs walks root in pre-order and assigns sequential NodeIDs to
// every Node that carries an `ID NodeID` field. Existing nonzero IDs
// are preserved so repeated calls are idempotent after the first pass.
// The first assigned ID is 1; 0 remains the "unassigned" sentinel.
//
// Uses reflection to avoid a hand-written switch over every Node type;
// parse-time cost is negligible relative to I/O and selfhost lowering.
func AssignIDs(root Node) {
	if root == nil {
		return
	}
	next := NodeID(1)
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface, reflect.Ptr:
			if v.IsNil() {
				return
			}
			walk(v.Elem())
			return
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
			return
		case reflect.Struct:
			// ok — fall through
		default:
			return
		}
		if idField := v.FieldByName("ID"); idField.IsValid() &&
			idField.Type() == nodeIDType && idField.CanSet() {
			if idField.Uint() == 0 {
				idField.SetUint(uint64(next))
				next++
			}
		}
		for i := 0; i < v.NumField(); i++ {
			walk(v.Field(i))
		}
	}
	walk(reflect.ValueOf(root))
}

var nodeIDType = reflect.TypeOf(NodeID(0))
