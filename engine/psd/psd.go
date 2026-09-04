package psd

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"unicode/utf16"

	"photoslicer/engine/watermark"
)

func beU16(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

func beU32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

func beS16(v int16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(v))
	return b
}

func beS32(v int32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(v))
	return b
}

func pascalName(name string) []byte {
	if len(name) > 255 {
		name = name[:255]
	}
	raw := append([]byte{byte(len(name))}, []byte(name)...)
	pad := (4 - (len(raw) % 4)) % 4
	return append(raw, bytes.Repeat([]byte{0}, pad)...)
}

func unicodeNameBlock(name string) []byte {
	runes := []rune(name)
	u16 := utf16.Encode(runes)
	body := make([]byte, 4+len(u16)*2)
	binary.BigEndian.PutUint32(body[:4], uint32(len(u16)))
	for i, v := range u16 {
		binary.BigEndian.PutUint16(body[4+i*2:], v)
	}
	pad := (4 - (len(body) % 4)) % 4
	body = append(body, bytes.Repeat([]byte{0}, pad)...)

	res := append([]byte("8BIMluni"), beU32(uint32(len(body)))...)
	return append(res, body...)
}

func buildChannelDataZip(data []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, _ = w.Write(data)
	_ = w.Close()

	res := beU16(2) // Compression: 2 = zip
	return append(res, buf.Bytes()...)
}

type layerRecord struct {
	name   string
	top    int32
	left   int32
	bottom int32
	right  int32
	alpha  []byte
	r      []byte
	g      []byte
	b      []byte
}

func flattenToRGB(img image.Image) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	white := image.NewUniform(color.White)
	draw.Draw(dst, dst.Bounds(), white, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
	return dst
}

// SavePSDLayered writes a multi-layer PSD file with slice art on the base layer
// and watermarks as movable/editable layers on top.
func SavePSDLayered(
	baseImage image.Image,
	outputPath string,
	watermarkEnabled bool,
	watermarkPath string,
	watermarkCount int,
	watermarkEdge string,
	watermarkWidthPercent int,
	watermarkMargin int,
) error {
	baseRGB := flattenToRGB(baseImage)
	bounds := baseRGB.Bounds()
	w, h := int32(bounds.Dx()), int32(bounds.Dy())

	// Base art layer channels
	totalPixels := int(w * h)
	baseR := make([]byte, totalPixels)
	baseG := make([]byte, totalPixels)
	baseB := make([]byte, totalPixels)

	for y := int32(0); y < h; y++ {
		for x := int32(0); x < w; x++ {
			c := baseRGB.RGBAAt(int(x), int(y))
			idx := int(y*w + x)
			baseR[idx] = c.R
			baseG[idx] = c.G
			baseB[idx] = c.B
		}
	}

	allLayers := []layerRecord{
		{
			name:   "Art",
			top:    0,
			left:   0,
			bottom: h,
			right:  w,
			alpha:  nil,
			r:      baseR,
			g:      baseG,
			b:      baseB,
		},
	}

	if watermarkEnabled && watermarkPath != "" {
		placements, _ := watermark.ComputeWatermarkPlacements(baseRGB, watermarkPath, watermarkCount, watermarkEdge, watermarkWidthPercent, watermarkMargin)
		for idx, p := range placements {
			if p.Image == nil {
				continue
			}
			wm := p.Image
			ww, wh := int32(wm.Bounds().Dx()), int32(wm.Bounds().Dy())

			x := int32(p.X)
			y := int32(p.Y)
			if x < 0 {
				x = 0
			}
			if y < 0 {
				y = 0
			}
			right := x + ww
			if right > w {
				right = w
			}
			bottom := y + wh
			if bottom > h {
				bottom = h
			}

			if right <= x || bottom <= y {
				continue
			}

			cw := int(right - x)
			ch := int(bottom - y)
			cTotal := cw * ch

			wmA := make([]byte, cTotal)
			wmR := make([]byte, cTotal)
			wmG := make([]byte, cTotal)
			wmB := make([]byte, cTotal)

			srcXOffset := 0
			if p.X < 0 {
				srcXOffset = -p.X
			}
			srcYOffset := 0
			if p.Y < 0 {
				srcYOffset = -p.Y
			}

			wmBounds := wm.Bounds()
			for ly := 0; ly < ch; ly++ {
				for lx := 0; lx < cw; lx++ {
					c := wm.RGBAAt(wmBounds.Min.X+srcXOffset+lx, wmBounds.Min.Y+srcYOffset+ly)
					cIdx := ly*cw + lx
					wmA[cIdx] = c.A
					wmR[cIdx] = c.R
					wmG[cIdx] = c.G
					wmB[cIdx] = c.B
				}
			}

			name := p.Name
			if name == "" {
				name = fmt.Sprintf("Watermark %d", idx+1)
			}

			allLayers = append(allLayers, layerRecord{
				name:   name,
				top:    y,
				left:   x,
				bottom: bottom,
				right:  right,
				alpha:  wmA,
				r:      wmR,
				g:      wmG,
				b:      wmB,
			})
		}
	}

	// Build Layer Records & Channel Blobs
	var recordsBuf bytes.Buffer
	var channelBlobsBuf bytes.Buffer

	for _, layer := range allLayers {
		type chanInfo struct {
			id   int16
			data []byte
		}
		var chans []chanInfo

		if layer.alpha != nil {
			chans = append(chans, chanInfo{id: -1, data: buildChannelDataZip(layer.alpha)})
		}
		chans = append(chans, chanInfo{id: 0, data: buildChannelDataZip(layer.r)})
		chans = append(chans, chanInfo{id: 1, data: buildChannelDataZip(layer.g)})
		chans = append(chans, chanInfo{id: 2, data: buildChannelDataZip(layer.b)})

		// Record header
		recordsBuf.Write(beS32(layer.top))
		recordsBuf.Write(beS32(layer.left))
		recordsBuf.Write(beS32(layer.bottom))
		recordsBuf.Write(beS32(layer.right))
		recordsBuf.Write(beU16(uint16(len(chans))))

		for _, ch := range chans {
			recordsBuf.Write(beS16(ch.id))
			recordsBuf.Write(beU32(uint32(len(ch.data))))
			channelBlobsBuf.Write(ch.data)
		}

		recordsBuf.Write([]byte("8BIMnorm"))
		recordsBuf.Write([]byte{255, 0, 0, 0}) // opacity 255, clipping, flags, filler

		// Extra data
		var extra bytes.Buffer
		extra.Write(beU32(0)) // Layer mask data length

		// Blending ranges (5 ranges * 8 bytes = 40 bytes)
		var blendBuf bytes.Buffer
		for i := 0; i < 5; i++ {
			blendBuf.Write([]byte{0, 0, 255, 255, 0, 0, 255, 255})
		}
		extra.Write(beU32(uint32(blendBuf.Len())))
		extra.Write(blendBuf.Bytes())

		extra.Write(pascalName(layer.name))
		extra.Write(unicodeNameBlock(layer.name))

		recordsBuf.Write(beU32(uint32(extra.Len())))
		recordsBuf.Write(extra.Bytes())
	}

	// Layer Info block
	var layerInfo bytes.Buffer
	layerCount := int16(len(allLayers))
	layerInfo.Write(beS16(layerCount))
	layerInfo.Write(recordsBuf.Bytes())
	layerInfo.Write(channelBlobsBuf.Bytes())

	// Pad layer info to even length
	if layerInfo.Len()%2 != 0 {
		layerInfo.WriteByte(0)
	}

	// Layer and Mask section
	var lmSection bytes.Buffer
	lmSection.Write(beU32(uint32(layerInfo.Len())))
	lmSection.Write(layerInfo.Bytes())

	// Write complete PSD file
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}

	writeErr := func() error {
		// 1. Header (26 bytes)
		if _, err := f.Write([]byte("8BPS")); err != nil {
			return err
		}
		if _, err := f.Write(beU16(1)); err != nil {
			return err
		}
		if _, err := f.Write(make([]byte, 6)); err != nil {
			return err
		}
		if _, err := f.Write(beU16(3)); err != nil {
			return err
		}
		if _, err := f.Write(beU32(uint32(h))); err != nil {
			return err
		}
		if _, err := f.Write(beU32(uint32(w))); err != nil {
			return err
		}
		if _, err := f.Write(beU16(8)); err != nil {
			return err
		}
		if _, err := f.Write(beU16(3)); err != nil {
			return err
		}

		// 2. Color Mode Data
		if _, err := f.Write(beU32(0)); err != nil {
			return err
		}

		// 3. Image Resources
		if _, err := f.Write(beU32(0)); err != nil {
			return err
		}

		// 4. Layer and Mask Section
		if _, err := f.Write(beU32(uint32(lmSection.Len()))); err != nil {
			return err
		}
		if _, err := f.Write(lmSection.Bytes()); err != nil {
			return err
		}

		// 5. Global Image Data (Composite preview)
		// Compression code 0 (raw bytes): R plane, G plane, B plane
		if _, err := f.Write(beU16(0)); err != nil {
			return err
		}
		if _, err := f.Write(baseR); err != nil {
			return err
		}
		if _, err := f.Write(baseG); err != nil {
			return err
		}
		if _, err := f.Write(baseB); err != nil {
			return err
		}
		return nil
	}()

	if writeErr != nil {
		_ = f.Close()
		_ = os.Remove(outputPath)
		return writeErr
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(outputPath)
		return err
	}

	return nil
}
