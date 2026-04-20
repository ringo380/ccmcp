package stringslice

import (
	"reflect"
	"testing"
)

func TestSet(t *testing.T) {
	s := Set([]string{"a", "b", "a"})
	if !s["a"] || !s["b"] || s["c"] {
		t.Errorf("Set: %v", s)
	}
	if len(Set(nil)) != 0 {
		t.Error("Set(nil) should be empty")
	}
}

func TestContains(t *testing.T) {
	if !Contains([]string{"a", "b"}, "a") {
		t.Error("want true for present element")
	}
	if Contains([]string{"a", "b"}, "c") {
		t.Error("want false for missing element")
	}
	if Contains(nil, "a") {
		t.Error("nil slice: want false")
	}
}

func TestUniqueAppend(t *testing.T) {
	got := UniqueAppend([]string{"a", "b"}, "c")
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("append new: %v", got)
	}
	got = UniqueAppend([]string{"a", "b"}, "a")
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("append existing should no-op: %v", got)
	}
}

func TestRemove(t *testing.T) {
	got := Remove([]string{"a", "b", "c", "b"}, "b")
	if !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Errorf("remove all occurrences: %v", got)
	}
	got = Remove([]string{"a", "b"}, "z")
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("remove missing should no-op: %v", got)
	}
}
