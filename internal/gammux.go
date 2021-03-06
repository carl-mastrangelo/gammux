package internal

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"math"

	"golang.org/x/image/draw"
)

const (
	fullScaling = 2

	sourceGamma = 2.2 // this is the common default.  Use this since Go doesn't expose it.

	targetGamma = sourceGamma * 20

	nrgba64Max = 0xFFFF
	nrgbaMax   = 0xFF
)

var thumbnailDarkenFactor = math.Pow(math.Nextafter(0.5, 0)/nrgbaMax, sourceGamma/targetGamma)

type ErrChain struct {
	msg   string
	cause error
}

func (e *ErrChain) Error() string {
	msg := e.msg
	if e.cause != nil {
		msg += "\n\tCaused by\n" + e.cause.Error()
	}
	return msg
}

func ChainErr(cause error, message string) *ErrChain {
	return &ErrChain{
		msg:   message,
		cause: cause,
	}
}

func removeAlpha(src image.Image) *image.NRGBA64 {
	dst := image.NewNRGBA64(image.Rectangle{
		Max: image.Point{
			X: src.Bounds().Dx(),
			Y: src.Bounds().Dy(),
		},
	})
	var dsty int
	for y := src.Bounds().Min.Y; y < src.Bounds().Max.Y; y++ {
		var dstx int
		for x := src.Bounds().Min.X; x < src.Bounds().Max.X; x++ {
			px := color.NRGBA64Model.Convert(src.At(x, y)).(color.NRGBA64)
			if px.A != nrgba64Max {
				dst.SetNRGBA64(dstx, dsty, color.NRGBA64{
					R: uint16(uint32(px.R)*uint32(px.A)>>16 + nrgba64Max - uint32(px.A)),
					G: uint16(uint32(px.G)*uint32(px.A)>>16 + nrgba64Max - uint32(px.A)),
					B: uint16(uint32(px.B)*uint32(px.A)>>16 + nrgba64Max - uint32(px.A)),
					A: nrgba64Max,
				})
			} else {
				dst.SetNRGBA64(dstx, dsty, color.NRGBA64{
					R: px.R,
					G: px.G,
					B: px.B,
					A: nrgba64Max,
				})
			}
			dstx++
		}
		dsty++
	}
	return dst
}

// Linearize image.  At leats 16 bits per channel are needed as per
// http://lbodnar.dsl.pipex.com/imaging/gamma.html
func linearImage(srcim image.Image, gamma float64) *image.NRGBA64 {
	dstim := image.NewNRGBA64(image.Rectangle{
		Max: image.Point{
			X: srcim.Bounds().Dx(),
			Y: srcim.Bounds().Dy(),
		},
	})
	var dsty int
	for srcy := srcim.Bounds().Min.Y; srcy < srcim.Bounds().Max.Y; srcy++ {
		var dstx int
		for srcx := srcim.Bounds().Min.X; srcx < srcim.Bounds().Max.X; srcx++ {
			nrgba64 := color.NRGBA64Model.Convert(srcim.At(srcx, srcy)).(color.NRGBA64)
			nrgba64.R = uint16(nrgba64Max * math.Pow(float64(nrgba64.R)/nrgba64Max, gamma))
			nrgba64.G = uint16(nrgba64Max * math.Pow(float64(nrgba64.G)/nrgba64Max, gamma))
			nrgba64.B = uint16(nrgba64Max * math.Pow(float64(nrgba64.B)/nrgba64Max, gamma))
			// Alpha is not affected
			dstim.SetNRGBA64(dstx, dsty, nrgba64)
			dstx++
		}
		dsty++
	}
	return dstim
}

func darkenImage(srcim image.Image, scale float64) *image.NRGBA64 {
	dstim := image.NewNRGBA64(image.Rectangle{
		Max: image.Point{
			X: srcim.Bounds().Dx(),
			Y: srcim.Bounds().Dy(),
		},
	})
	var dsty int
	for srcy := srcim.Bounds().Min.Y; srcy < srcim.Bounds().Max.Y; srcy++ {
		var dstx int
		for srcx := srcim.Bounds().Min.X; srcx < srcim.Bounds().Max.X; srcx++ {
			nrgba64 := color.NRGBA64Model.Convert(srcim.At(srcx, srcy)).(color.NRGBA64)
			nrgba64.R = uint16(float64(nrgba64.R) * scale)
			nrgba64.G = uint16(float64(nrgba64.G) * scale)
			nrgba64.B = uint16(float64(nrgba64.B) * scale)
			// Alpha is not affected
			dstim.SetNRGBA64(dstx, dsty, nrgba64)
			dstx++
		}
		dsty++
	}
	return dstim
}

// Assumes src is linear
func resize(src image.Image, targetBounds image.Rectangle, targetScaleDown int, stretch bool) (
	*image.NRGBA64, int, int) {
	var xoffset, yoffset int
	var newTargetBounds image.Rectangle
	if stretch {
		newTargetBounds = image.Rectangle{
			Max: image.Point{
				X: targetBounds.Dx() / targetScaleDown,
				Y: targetBounds.Dy() / targetScaleDown,
			},
		}
	} else {
		// Check if the source image is wider than the dest, or narrower.   The odd multiplication
		// avoids casting to float, at the risk of possibly overflow.  Don't use images taller or
		// wider than 32K on 32 bits machines.
		if src.Bounds().Dx()*targetBounds.Dy() > targetBounds.Dx()*src.Bounds().Dy() {
			// source image is wider.
			newTargetBounds = image.Rectangle{
				Max: image.Point{
					X: targetBounds.Dx() / targetScaleDown,
					Y: src.Bounds().Dy() * targetBounds.Dx() / src.Bounds().Dx() / targetScaleDown,
				},
			}
			yoffset = (targetBounds.Dy() - newTargetBounds.Dy()*targetScaleDown) / 2
		} else {
			// source image is narrower.
			newTargetBounds = image.Rectangle{
				Max: image.Point{
					X: src.Bounds().Dx() * targetBounds.Dy() / src.Bounds().Dy() / targetScaleDown,
					Y: targetBounds.Dy() / targetScaleDown,
				},
			}
			xoffset = (targetBounds.Dx() - newTargetBounds.Dx()*targetScaleDown) / 2
		}
	}

	dst := image.NewNRGBA64(newTargetBounds)
	scaler := draw.CatmullRom
	scaler.Scale(dst, newTargetBounds, src, src.Bounds(), draw.Over, nil)
	return dst, xoffset, yoffset
}

type dithererr struct {
	r, g, b float64
}

func scaleClamp(v float64, max float64) float64 {
	if v > 1.0 {
		v = 1.0
	}
	return math.Round(v * max)
}

func calculateFullPixel(
	srcx int, srcnrgba color.NRGBA64, dither bool, errcurr, errnext []dithererr) color.NRGBA {
	const newMaxValue = nrgbaMax
	nonneg := func(in float64) float64 {
		if low := 1.0 / newMaxValue; in < low {
			return low
		}
		return in
	}

	var (
		// Make sure there are no zeros
		red   = float64(srcnrgba.R) / nrgba64Max
		green = float64(srcnrgba.G) / nrgba64Max
		blue  = float64(srcnrgba.B) / nrgba64Max

		// Apply the previous error
		// clamp pixel to minimum value.  This avoids a black mesh if the input pixel is black.
		// Also, if there is a row of black pixels, the error can build up.  By clamping, negative
		// will not get excessive.  (this consumes the first bright pixel after a string of dark
		// pixels otherwise).
		errorred   = nonneg(red + errcurr[srcx+1].r)
		errorgreen = nonneg(green + errcurr[srcx+1].g)
		errorblue  = nonneg(blue + errcurr[srcx+1].b)

		// apply the new gamma
		newred   = math.Pow(errorred, 1/targetGamma)
		newgreen = math.Pow(errorgreen, 1/targetGamma)
		newblue  = math.Pow(errorblue, 1/targetGamma)

		// bring value up to 0-newMaxValue range
		roundred   = scaleClamp(newred, newMaxValue)
		roundgreen = scaleClamp(newgreen, newMaxValue)
		roundblue  = scaleClamp(newblue, newMaxValue)
	)

	if dither {
		// Undo the gamma transform once more to make the error linear
		var (
			diffred   = errorred - math.Pow(roundred/newMaxValue, targetGamma)
			diffgreen = errorgreen - math.Pow(roundgreen/newMaxValue, targetGamma)
			diffblue  = errorblue - math.Pow(roundblue/newMaxValue, targetGamma)
		)

		errcurr[srcx+2].r += diffred * 7 / 16
		errcurr[srcx+2].g += diffgreen * 7 / 16
		errcurr[srcx+2].b += diffblue * 7 / 16

		errnext[srcx].r += diffred * 3 / 16
		errnext[srcx].g += diffgreen * 3 / 16
		errnext[srcx].b += diffblue * 3 / 16

		errnext[srcx+1].r += diffred * 5 / 16
		errnext[srcx+1].g += diffgreen * 5 / 16
		errnext[srcx+1].b += diffblue * 5 / 16

		errnext[srcx+2].r += diffred * 1 / 16
		errnext[srcx+2].g += diffgreen * 1 / 16
		errnext[srcx+2].b += diffblue * 1 / 16
	}
	return color.NRGBA{
		R: uint8(roundred),
		G: uint8(roundgreen),
		B: uint8(roundblue),
		A: uint8(srcnrgba.A >> 8),
	}
}

func GammaMuxImages(thumbnail, full image.Image, dither, stretch bool) (image.Image, *ErrChain) {
	noOffsetThumbnailRec := image.Rectangle{
		Max: image.Point{
			X: thumbnail.Bounds().Dx(),
			Y: thumbnail.Bounds().Dy(),
		},
	}

	// linearize before resizing
	linearfull := linearImage(removeAlpha(full), sourceGamma)
	// Always resize, regardless of dimensions
	smallfull, xoffset, yoffset := resize(linearfull, noOffsetThumbnailRec, fullScaling, stretch)
	// thumbnailDarkenFactor is a max value that will turn to black after the gamma transform
	darkThumbnail := darkenImage(removeAlpha(thumbnail), thumbnailDarkenFactor)
	var errcurr, errnext []dithererr
	errnext = make([]dithererr, smallfull.Bounds().Dx()+2)

	dst := image.NewNRGBA(noOffsetThumbnailRec)

	for srcy := 0; srcy < dst.Bounds().Max.Y; srcy++ {
		for srcx := 0; srcx < dst.Bounds().Max.X; srcx++ {
			dst.SetNRGBA(srcx, srcy, color.NRGBAModel.Convert(darkThumbnail.NRGBA64At(srcx, srcy)).(color.NRGBA))
		}
	}

	dsty := yoffset
	for srcy := smallfull.Bounds().Min.Y; srcy < smallfull.Bounds().Max.Y; srcy++ {
		errcurr = errnext
		errnext = make([]dithererr, smallfull.Bounds().Dx()+2)
		dstx := xoffset
		for srcx := smallfull.Bounds().Min.X; srcx < smallfull.Bounds().Max.X; srcx++ {
			srcnrgba := color.NRGBA64Model.Convert(smallfull.At(srcx, srcy)).(color.NRGBA64)
			newFullPixel := calculateFullPixel(srcx, srcnrgba, dither, errcurr, errnext)

			thumbeast, thumbsouth, thumbsoutheast := removeHalo(
				color.NRGBA64Model.Convert(newFullPixel).(color.NRGBA64),
				darkThumbnail.NRGBA64At(dstx, dsty),
				darkThumbnail.NRGBA64At(dstx+1, dsty),
				darkThumbnail.NRGBA64At(dstx, dsty+1),
				darkThumbnail.NRGBA64At(dstx+1, dsty+1))

			dst.SetNRGBA(dstx, dsty, newFullPixel)
			dst.SetNRGBA(dstx+1, dsty, thumbeast)
			dst.SetNRGBA(dstx, dsty+1, thumbsouth)
			dst.SetNRGBA(dstx+1, dsty+1, thumbsoutheast)
			dstx += fullScaling
		}
		dsty += fullScaling
	}

	return dst, nil
}

// Do averaging using the arithmetic mean, since that's what the decoder will (wrongly) do.
func removeHalo(full, thumb, thumbeast, thumbsouth, thumbsoutheast color.NRGBA64) (
	newthumbeast, newthumbsouth, newthumbsoutheast color.NRGBA) {
	clampround := func(val float64) uint8 {
		v := math.Round(val) / 256
		if v > thumbnailDarkenFactor*nrgbaMax {
			return uint8(thumbnailDarkenFactor * nrgbaMax)
		} else if v < 0 {
			return 0
		}
		return uint8(v)
	}

	var (
		rdenom  = float64(thumbeast.R) + float64(thumbsouth.R) + float64(thumbsoutheast.R)
		rfactor = (rdenom + float64(thumb.R) - float64(full.R)) / rdenom

		gdenom  = float64(thumbeast.G) + float64(thumbsouth.G) + float64(thumbsoutheast.G)
		gfactor = (gdenom + float64(thumb.G) - float64(full.G)) / gdenom

		bdenom  = float64(thumbeast.B) + float64(thumbsouth.B) + float64(thumbsoutheast.B)
		bfactor = (bdenom + float64(thumb.B) - float64(full.B)) / bdenom
	)

	newthumbeast = color.NRGBA{
		R: clampround(float64(thumbeast.R) * rfactor),
		G: clampround(float64(thumbeast.G) * gfactor),
		B: clampround(float64(thumbeast.B) * bfactor),
		A: uint8(thumbeast.A >> 8),
	}
	newthumbsouth = color.NRGBA{
		R: clampround(float64(thumbsouth.R) * rfactor),
		G: clampround(float64(thumbsouth.G) * gfactor),
		B: clampround(float64(thumbsouth.B) * bfactor),
		A: uint8(thumbsouth.A >> 8),
	}
	newthumbsoutheast = color.NRGBA{
		R: clampround(float64(thumbsoutheast.R) * rfactor),
		G: clampround(float64(thumbsoutheast.G) * gfactor),
		B: clampround(float64(thumbsoutheast.B) * bfactor),
		A: uint8(thumbsoutheast.A >> 8),
	}

	return newthumbeast, newthumbsouth, newthumbsoutheast
}

func GammaMuxData(thumbnail, full io.Reader, dest io.Writer, dither, stretch bool) *ErrChain {
	// sadly, Go's own decoder does not handle Gamma properly.  This program shares shame
	// with all the other non-compliant renderers.
	tim, _, err := image.Decode(thumbnail)
	if err != nil {
		return ChainErr(err, "Unable to decode thumbnail")
	}
	fim, _, err := image.Decode(full)
	if err != nil {
		return ChainErr(err, "Unable to decode full")
	}

	dim, ec := GammaMuxImages(tim, fim, dither, stretch)
	if ec != nil {
		return ec
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, dim); err != nil {
		return ChainErr(err, "Unable to encode dest PNG")
	}

	headerIndex := bytes.Index(buf.Bytes(), []byte{0, 0, 0, 13, 'I', 'H', 'D', 'R'})
	if headerIndex <= 0 {
		return ChainErr(nil, "PNG missing header")
	}
	headerIndexEnd := headerIndex + 13 + 4 + 4 + 4

	if _, err := dest.Write(buf.Bytes()[:headerIndexEnd]); err != nil {
		return ChainErr(err, "Unable to write PNG header")
	}
	if ec := writeGamaPngChunk(dest, targetGamma); ec != nil {
		return ec
	}
	if _, err := dest.Write(buf.Bytes()[headerIndexEnd:]); err != nil {
		return ChainErr(err, "Unable to write PNG header")
	}

	return nil
}

func writeGamaPngChunk(w io.Writer, gamma float64) *ErrChain {
	gamaBuf := make([]byte, 4+4+4+4)
	copy(gamaBuf, []byte{0, 0, 0, 4, 'g', 'A', 'M', 'A'})
	binary.BigEndian.PutUint32(gamaBuf[8:12], uint32(math.Round(100000/gamma)))
	crc := crc32.NewIEEE()
	crc.Write(gamaBuf[4:12])
	binary.BigEndian.PutUint32(gamaBuf[12:16], crc.Sum32())
	if _, err := w.Write(gamaBuf); err != nil {
		return ChainErr(err, "Unable to write PNG gAMA chunk")
	}
	return nil
}
