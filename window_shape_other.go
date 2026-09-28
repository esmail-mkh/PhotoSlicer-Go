//go:build !windows

package main

// applyWindowShape does nothing here: frameless windows are drawn square by the
// window system, and only the rim in the frontend hints at the rounded design.
func applyWindowShape(radius int) {}
