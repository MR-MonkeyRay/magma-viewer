//go:build !unix

package store

func dirWritable(string) error { return nil }
