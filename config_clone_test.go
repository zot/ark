package ark

// CRC: crc-Config.md | Test: test-ConfigClone.md | R3673, R3674

import (
	"reflect"
	"testing"
	"unsafe"
)

// TestConfigCloneSharesNothing fills every slice, map and pointer reachable in
// a Config (unexported fields included) and checks that Clone shares none of
// them. A field added later that Clone fails to copy is shared, and so written
// under a reader holding the old Config, with nothing else failing: this is
// what notices. It also checks the clone is equal to the original, so the copy
// is a copy and not merely different.
func TestConfigCloneSharesNothing(t *testing.T) {
	c := &Config{}
	fill(reflect.ValueOf(c).Elem(), 0)
	// A fill that reached nothing would pass vacuously.
	if n := countRefs(reflect.ValueOf(c).Elem()); n < 20 {
		t.Fatalf("only %d references were filled; the test is not reaching the Config's fields", n)
	}
	clone := c.Clone()
	if !reflect.DeepEqual(c, clone) {
		t.Fatal("Clone is not equal to its original")
	}
	var shared []string
	checkShared(reflect.ValueOf(c).Elem(), reflect.ValueOf(clone).Elem(), "Config", &shared)
	for _, path := range shared {
		t.Errorf("Clone shares %s with its original", path)
	}
}

// settable returns v made writable even when it is an unexported field.
func settable(v reflect.Value) reflect.Value {
	if v.CanSet() || !v.CanAddr() {
		return v
	}
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
}

// fill gives every nil slice, map and pointer under v one element, recursively,
// so the shared-reference walk has something to compare at every depth.
func fill(v reflect.Value, depth int) {
	if depth > 8 {
		return
	}
	v = settable(v)
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		fill(v.Elem(), depth+1)
	case reflect.Slice:
		if v.Len() == 0 {
			v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		}
		for i := 0; i < v.Len(); i++ {
			fill(v.Index(i), depth+1)
		}
	case reflect.Map:
		if v.IsNil() {
			v.Set(reflect.MakeMap(v.Type()))
		}
		if v.Len() == 0 {
			k := reflect.New(v.Type().Key()).Elem()
			e := reflect.New(v.Type().Elem()).Elem()
			fill(e, depth+1)
			v.SetMapIndex(k, e)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			fill(v.Field(i), depth+1)
		}
	case reflect.String:
		if v.String() == "" {
			v.SetString("x")
		}
	}
}

// checkShared walks a and b in step and records the path of every slice, map
// or pointer they share.
func checkShared(a, b reflect.Value, path string, shared *[]string) {
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() {
			return
		}
		if a.Pointer() == b.Pointer() {
			*shared = append(*shared, path)
			return
		}
		checkShared(a.Elem(), b.Elem(), path, shared)
	case reflect.Slice:
		if a.Len() == 0 {
			return
		}
		if a.Pointer() == b.Pointer() {
			*shared = append(*shared, path)
			return
		}
		for i := 0; i < a.Len(); i++ {
			checkShared(a.Index(i), b.Index(i), path+"[]", shared)
		}
	case reflect.Map:
		if a.Len() == 0 {
			return
		}
		if a.Pointer() == b.Pointer() {
			*shared = append(*shared, path)
			return
		}
		for it := a.MapRange(); it.Next(); {
			checkShared(it.Value(), b.MapIndex(it.Key()), path+"{}", shared)
		}
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			checkShared(a.Field(i), b.Field(i), path+"."+a.Type().Field(i).Name, shared)
		}
	}
}

// countRefs counts the non-empty slices, maps and pointers under v.
func countRefs(v reflect.Value) int {
	n := 0
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			n = 1 + countRefs(v.Elem())
		}
	case reflect.Slice:
		if v.Len() > 0 {
			n = 1
			for i := 0; i < v.Len(); i++ {
				n += countRefs(v.Index(i))
			}
		}
	case reflect.Map:
		if v.Len() > 0 {
			n = 1
			for it := v.MapRange(); it.Next(); {
				n += countRefs(it.Value())
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			n += countRefs(v.Field(i))
		}
	}
	return n
}
