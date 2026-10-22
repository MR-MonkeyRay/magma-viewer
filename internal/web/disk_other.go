//go:build !linux

package web

func diskSpace(string) *disk { return nil }
