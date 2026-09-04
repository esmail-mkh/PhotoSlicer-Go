package imageio

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
)

// DecodePSDComposite decodes the flattened composite image from an Adobe Photoshop PSD file.
func DecodePSDComposite(r io.Reader) (image.Image, error) {
	// 1. Header (26 bytes)
	var header [26]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, fmt.Errorf("failed to read PSD header: %w", err)
	}

	if string(header[:4]) != "8BPS" {
		return nil, fmt.Errorf("not a valid PSD file: missing 8BPS signature")
	}

	version := binary.BigEndian.Uint16(header[4:6])
	if version != 1 {
		return nil, fmt.Errorf("unsupported PSD version: %d (only v1 supported)", version)
	}

	channels := int(binary.BigEndian.Uint16(header[12:14]))
	height := int(binary.BigEndian.Uint32(header[14:18]))
	width := int(binary.BigEndian.Uint32(header[18:22]))
	depth := int(binary.BigEndian.Uint16(header[22:24]))
	colorMode := binary.BigEndian.Uint16(header[24:26])

	if width <= 0 || height <= 0 || width > 30000 || height > 30000 {
		return nil, fmt.Errorf("invalid PSD dimensions: %dx%d (maximum supported: 30000x30000)", width, height)
	}
	if int64(width)*int64(height) > 100_000_000 {
		return nil, fmt.Errorf("PSD image exceeds maximum allowed pixel count: %d pixels", int64(width)*int64(height))
	}
	if channels < 1 || channels > 56 {
		return nil, fmt.Errorf("invalid PSD channel count: %d (expected 1 to 56)", channels)
	}
	if depth != 8 && depth != 16 {
		return nil, fmt.Errorf("unsupported PSD bit depth: %d (only 8-bit and 16-bit supported)", depth)
	}
	// Color mode: 1 = Grayscale, 2 = Indexed, 3 = RGB, 4 = CMYK
	if colorMode != 1 && colorMode != 2 && colorMode != 3 && colorMode != 4 {
		return nil, fmt.Errorf("unsupported PSD color mode: %d", colorMode)
	}
	if colorMode == 3 && channels < 3 {
		return nil, fmt.Errorf("insufficient channels for RGB mode: %d (expected at least 3)", channels)
	}
	if colorMode == 4 && channels < 4 {
		return nil, fmt.Errorf("insufficient channels for CMYK mode: %d (expected at least 4)", channels)
	}

	// Helper to skip sections with 32-bit length prefix
	skipSection := func() error {
		var lenBuf [4]byte
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			return err
		}
		length := int64(binary.BigEndian.Uint32(lenBuf[:]))
		if length > 0 {
			_, err := io.CopyN(io.Discard, r, length)
			return err
		}
		return nil
	}

	// 2. Color Mode Data
	var palette []byte
	var cmLenBuf [4]byte
	if _, err := io.ReadFull(r, cmLenBuf[:]); err != nil {
		return nil, fmt.Errorf("error reading color mode section length: %w", err)
	}
	cmLength := int64(binary.BigEndian.Uint32(cmLenBuf[:]))
	if cmLength > 0 {
		if colorMode == 2 && cmLength == 768 {
			palette = make([]byte, 768)
			if _, err := io.ReadFull(r, palette); err != nil {
				return nil, fmt.Errorf("error reading indexed palette: %w", err)
			}
		} else {
			if _, err := io.CopyN(io.Discard, r, cmLength); err != nil {
				return nil, fmt.Errorf("error skipping color mode data: %w", err)
			}
		}
	}

	// 3. Image Resources
	if err := skipSection(); err != nil {
		return nil, fmt.Errorf("error reading image resources section: %w", err)
	}

	// 4. Layer and Mask Information
	// Adobe Photoshop specification:
	// If layer count is a negative number, its absolute value is the number of layers
	// and the first alpha channel contains the transparency data for the merged result.
	// If layer count is positive or zero, the composite contains NO merged transparency data.
	hasTransparency := false
	var lmLenBuf [4]byte
	if _, err := io.ReadFull(r, lmLenBuf[:]); err != nil {
		return nil, fmt.Errorf("error reading layer and mask section length: %w", err)
	}
	lmLength := int64(binary.BigEndian.Uint32(lmLenBuf[:]))
	if lmLength > 0 {
		bytesRead := int64(0)
		var liLenBuf [4]byte
		if _, err := io.ReadFull(r, liLenBuf[:]); err == nil {
			bytesRead += 4
			liLength := int64(binary.BigEndian.Uint32(liLenBuf[:]))
			if liLength > 0 {
				var lcBuf [2]byte
				if _, err := io.ReadFull(r, lcBuf[:]); err == nil {
					bytesRead += 2
					layerCount := int16(binary.BigEndian.Uint16(lcBuf[:]))
					if layerCount < 0 {
						hasTransparency = true
					}
				}
			}
		}
		remaining := lmLength - bytesRead
		if remaining > 0 {
			if _, err := io.CopyN(io.Discard, r, remaining); err != nil {
				return nil, fmt.Errorf("error skipping layer and mask section: %w", err)
			}
		}
	}

	// 5. Image Data (Composite preview)
	var compBuf [2]byte
	if _, err := io.ReadFull(r, compBuf[:]); err != nil {
		return nil, fmt.Errorf("failed to read image compression header: %w", err)
	}
	compression := binary.BigEndian.Uint16(compBuf[:])

	totalPixels := width * height
	bytesPerSample := depth / 8 // 1 or 2
	bytesPerChannel := totalPixels * bytesPerSample

	channelData := make([][]byte, channels)
	for c := 0; c < channels; c++ {
		channelData[c] = make([]byte, totalPixels)
	}

	switch compression {
	case 0:
		// Raw bytes: sequentially each channel
		for c := 0; c < channels; c++ {
			if bytesPerSample == 1 {
				if _, err := io.ReadFull(r, channelData[c]); err != nil {
					return nil, fmt.Errorf("failed to read raw channel data: %w", err)
				}
			} else {
				// 16-bit: read 2 bytes per pixel, keep high byte
				rawBuf := make([]byte, bytesPerChannel)
				if _, err := io.ReadFull(r, rawBuf); err != nil {
					return nil, fmt.Errorf("failed to read raw 16-bit channel data: %w", err)
				}
				for i := 0; i < totalPixels; i++ {
					channelData[c][i] = rawBuf[i*2]
				}
			}
		}
	case 1:
		// RLE (PackBits): read scanline lengths (height * channels * 2 bytes)
		numScanlines := height * channels
		scanlineLengths := make([]uint16, numScanlines)
		for i := 0; i < numScanlines; i++ {
			var lBuf [2]byte
			if _, err := io.ReadFull(r, lBuf[:]); err != nil {
				return nil, fmt.Errorf("failed to read RLE scanline lengths: %w", err)
			}
			scanlineLengths[i] = binary.BigEndian.Uint16(lBuf[:])
		}

		lineIdx := 0
		for c := 0; c < channels; c++ {
			for y := 0; y < height; y++ {
				lineLen := int(scanlineLengths[lineIdx])
				lineIdx++

				compressed := make([]byte, lineLen)
				if _, err := io.ReadFull(r, compressed); err != nil {
					return nil, fmt.Errorf("failed to read compressed scanline: %w", err)
				}

				lineOffset := y * width
				srcIdx := 0
				linePixelsWritten := 0

				if bytesPerSample == 1 {
					for srcIdx < lineLen && linePixelsWritten < width {
						b := int8(compressed[srcIdx])
						srcIdx++
						if b >= 0 {
							count := int(b) + 1
							for k := 0; k < count && srcIdx < lineLen && linePixelsWritten < width; k++ {
								channelData[c][lineOffset+linePixelsWritten] = compressed[srcIdx]
								srcIdx++
								linePixelsWritten++
							}
						} else if b != -128 {
							count := 1 - int(b)
							if srcIdx < lineLen {
								val := compressed[srcIdx]
								srcIdx++
								for k := 0; k < count && linePixelsWritten < width; k++ {
									channelData[c][lineOffset+linePixelsWritten] = val
									linePixelsWritten++
								}
							}
						}
					}
				} else {
					// 16-bit PackBits: each sample is 2 bytes, take high byte
					sampleByte := 0
					for srcIdx < lineLen && linePixelsWritten < width {
						b := int8(compressed[srcIdx])
						srcIdx++
						if b >= 0 {
							count := int(b) + 1
							for k := 0; k < count && srcIdx < lineLen && linePixelsWritten < width; k++ {
								val := compressed[srcIdx]
								srcIdx++
								if sampleByte%2 == 0 {
									channelData[c][lineOffset+linePixelsWritten] = val
									linePixelsWritten++
								}
								sampleByte++
							}
						} else if b != -128 {
							count := 1 - int(b)
							if srcIdx < lineLen {
								val := compressed[srcIdx]
								srcIdx++
								for k := 0; k < count && linePixelsWritten < width; k++ {
									if sampleByte%2 == 0 {
										channelData[c][lineOffset+linePixelsWritten] = val
										linePixelsWritten++
									}
									sampleByte++
								}
							}
						}
					}
				}
			}
		}
	default:
		return nil, fmt.Errorf("unsupported PSD compression mode: %d", compression)
	}

	// Determine if composite has a valid transparency channel
	alphaChanIdx := -1
	if hasTransparency {
		switch colorMode {
		case 1: // Grayscale
			if channels >= 2 {
				alphaChanIdx = 1
			}
		case 3: // RGB
			if channels >= 4 {
				alphaChanIdx = 3
			}
		case 4: // CMYK
			if channels >= 5 {
				alphaChanIdx = 4
			}
		}
	}

	// If transparency channel is all zeros (empty/broken mask), treat image as fully opaque
	if alphaChanIdx >= 0 {
		hasNonZero := false
		for _, v := range channelData[alphaChanIdx] {
			if v > 0 {
				hasNonZero = true
				break
			}
		}
		if !hasNonZero {
			alphaChanIdx = -1
		}
	}

	dst := image.NewRGBA(image.Rect(0, 0, width, height))

	switch colorMode {
	case 1: // Grayscale
		for i := 0; i < totalPixels; i++ {
			g := channelData[0][i]
			a := uint8(255)
			if alphaChanIdx >= 0 {
				a = channelData[alphaChanIdx][i]
			}
			dst.Pix[i*4] = g
			dst.Pix[i*4+1] = g
			dst.Pix[i*4+2] = g
			dst.Pix[i*4+3] = a
		}

	case 2: // Indexed
		for i := 0; i < totalPixels; i++ {
			idx := int(channelData[0][i])
			rVal, gVal, bVal := idx, idx, idx
			if len(palette) >= 768 {
				rVal = int(palette[idx])
				gVal = int(palette[256+idx])
				bVal = int(palette[512+idx])
			}
			dst.Pix[i*4] = uint8(rVal)
			dst.Pix[i*4+1] = uint8(gVal)
			dst.Pix[i*4+2] = uint8(bVal)
			dst.Pix[i*4+3] = 255
		}

	case 4: // CMYK
		for i := 0; i < totalPixels; i++ {
			c := channelData[0][i]
			m := channelData[1][i]
			y := channelData[2][i]
			k := channelData[3][i]
			rVal, gVal, bVal := color.CMYKToRGB(c, m, y, k)
			a := uint8(255)
			if alphaChanIdx >= 0 {
				a = channelData[alphaChanIdx][i]
				if a < 255 {
					rVal = uint8((uint32(rVal)*uint32(a) + 127) / 255)
					gVal = uint8((uint32(gVal)*uint32(a) + 127) / 255)
					bVal = uint8((uint32(bVal)*uint32(a) + 127) / 255)
				}
			}
			dst.Pix[i*4] = rVal
			dst.Pix[i*4+1] = gVal
			dst.Pix[i*4+2] = bVal
			dst.Pix[i*4+3] = a
		}

	case 3: // RGB
		fallthrough
	default:
		for i := 0; i < totalPixels; i++ {
			if alphaChanIdx >= 0 {
				a := channelData[alphaChanIdx][i]
				dst.Pix[i*4+3] = a
				if a < 255 {
					dst.Pix[i*4] = uint8((uint32(channelData[0][i])*uint32(a) + 127) / 255)
					dst.Pix[i*4+1] = uint8((uint32(channelData[1][i])*uint32(a) + 127) / 255)
					dst.Pix[i*4+2] = uint8((uint32(channelData[2][i])*uint32(a) + 127) / 255)
				} else {
					dst.Pix[i*4] = channelData[0][i]
					dst.Pix[i*4+1] = channelData[1][i]
					dst.Pix[i*4+2] = channelData[2][i]
				}
			} else {
				dst.Pix[i*4] = channelData[0][i]
				dst.Pix[i*4+1] = channelData[1][i]
				dst.Pix[i*4+2] = channelData[2][i]
				dst.Pix[i*4+3] = 255
			}
		}
	}

	return dst, nil
}
