package main

import (
	"mime"
	"testing"
)

func TestMimeOverride(t *testing.T) {
	mime.AddExtensionType(".mkv", "video/webm")
	mt := mime.TypeByExtension(".mkv")

	if mt != "video/webm" {
		t.Fatalf("Failed %s", mt)
	}
}
