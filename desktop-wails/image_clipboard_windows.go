//go:build windows

package main

import (
	"encoding/binary"
	"errors"
	"golang.org/x/sys/windows"
	"image"
	"unsafe"
)

var clipboardUser32 = windows.NewLazySystemDLL("user32.dll")
var clipboardKernel32 = windows.NewLazySystemDLL("kernel32.dll")

// CF_DIB stores a BITMAPINFOHEADER followed by bottom-up BGRA pixels.
func copyImageWindows(img image.Image, owner uintptr) (bool, error) {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	size := 40 + width*height*4
	mem, _, err := clipboardKernel32.NewProc("GlobalAlloc").Call(0x42, uintptr(size))
	if mem == 0 {
		return false, err
	}
	owned := true
	defer func() {
		if owned {
			clipboardKernel32.NewProc("GlobalFree").Call(mem)
		}
	}()
	ptr, _, err := clipboardKernel32.NewProc("GlobalLock").Call(mem)
	if ptr == 0 {
		return false, err
	}
	raw := make([]byte, size)
	binary.LittleEndian.PutUint32(raw[0:4], 40)
	binary.LittleEndian.PutUint32(raw[4:8], uint32(width))
	binary.LittleEndian.PutUint32(raw[8:12], uint32(height))
	binary.LittleEndian.PutUint16(raw[12:14], 1)
	binary.LittleEndian.PutUint16(raw[14:16], 32)
	binary.LittleEndian.PutUint32(raw[20:24], uint32(width*height*4))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+height-1-y).RGBA()
			at := 40 + (y*width+x)*4
			raw[at] = byte(b >> 8)
			raw[at+1] = byte(g >> 8)
			raw[at+2] = byte(r >> 8)
			raw[at+3] = 255
		}
	}
	clipboardKernel32.NewProc("RtlMoveMemory").Call(ptr, uintptr(unsafe.Pointer(&raw[0])), uintptr(size))
	clipboardKernel32.NewProc("GlobalUnlock").Call(mem)
	ok, _, err := clipboardUser32.NewProc("OpenClipboard").Call(owner)
	if ok == 0 {
		return false, err
	}
	defer clipboardUser32.NewProc("CloseClipboard").Call()
	ok, _, err = clipboardUser32.NewProc("EmptyClipboard").Call()
	if ok == 0 {
		return false, err
	}
	ok, _, err = clipboardUser32.NewProc("SetClipboardData").Call(8, mem)
	if ok == 0 {
		return false, errors.New("cannot write image clipboard: " + err.Error())
	}
	owned = false
	return true, nil
}
