//go:build !tiana_embedded

package releaseasset

func Current() Assets { return Assets{} }
