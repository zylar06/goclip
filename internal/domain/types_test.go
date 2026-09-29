package domain

import (
	"math"
	"testing"
)

func TestDraftValidation(t *testing.T) {
	d := NewDraft("测试", []Scene{{ID: ID(), Start: 1, End: 5}})
	if e := d.Validate(10); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Draft){
		func(d *Draft) { d.Scenes[0].End = 11 },
		func(d *Draft) { d.CropX = math.NaN() },
		func(d *Draft) { d.TitleStyle = "arbitrary" },
		func(d *Draft) { d.Scenes = nil },
		func(d *Draft) { d.Title = " " },
		func(d *Draft) { d.TitleTemplateVersion = 4 },
	} {
		c := d
		c.Scenes = append([]Scene{}, d.Scenes...)
		mutate(&c)
		if c.Validate(10) == nil {
			t.Fatal("invalid draft accepted", c)
		}
	}
	if ValidID("../data") {
		t.Fatal("traversal accepted")
	}
}
