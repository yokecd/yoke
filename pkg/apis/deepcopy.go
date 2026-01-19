package apis

import "reflect"

// DeepCopy uses reflection to deep copy an object.
// This is useful for add a naive implementation fo DeepCopyObject to satisfy the runtime.Object interface for your APIs
// should you desire to use your APIs with controllerruntime or other k8s.io apis.
//
// The limitation of this implementation is that it does not support cyclical data, but since these types are expected to be serializable,
// they shouldn't any way.
//
// Similarly funcs and channels are not supported as they also cannot be serialized safely.
func DeepCopy[T any](in T) T {
	return deepCopy(reflect.ValueOf(in)).Interface().(T)
}

func deepCopy(v reflect.Value) reflect.Value {
	switch t := v.Type(); t.Kind() {
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		m := reflect.MakeMap(t)
		iter := v.MapRange()
		for iter.Next() {
			m.SetMapIndex(deepCopy(iter.Key()), deepCopy(iter.Value()))
		}
		return m
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		s := reflect.MakeSlice(t, v.Len(), v.Cap())
		for i := range v.Len() {
			s.Index(i).Set(deepCopy(v.Index(i)))
		}
		return s
	case reflect.Array:
		a := reflect.New(reflect.ArrayOf(v.Len(), t.Elem())).Elem()
		for i := range v.Len() {
			a.Index(i).Set(deepCopy(v.Index(i)))
		}
		return a
	case reflect.Struct:
		s := reflect.New(t).Elem()
		for i := range v.NumField() {
			if !t.Field(i).IsExported() {
				continue
			}
			fv := v.Field(i)
			s.Field(i).Set(deepCopy(fv))
		}
		return s
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		p := reflect.New(t.Elem())
		p.Elem().Set(deepCopy(v.Elem()))
		return p
	case reflect.Chan:
		panic("channels are not supported: not serializable")
	case reflect.Func:
		panic("funcs are not supported: not serializable")
	default:
		return v
	}
}
