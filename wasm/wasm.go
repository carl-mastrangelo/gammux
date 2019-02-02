package main

import (
	"bytes"
	"encoding/base64"
	"syscall/js"

	"../internal"
)

const noImageData = "data:image/gif;base64,R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs="

type fileOrErr struct {
	data []byte
	err  error
}

func watchFile(elementId string) <-chan fileOrErr {
	res := make(chan fileOrErr)
	cb := js.NewCallback(func(args []js.Value) {
		files := args[0].Get("target").Get("files")
		if files.Get("length").Int() != 1 {
			res <- fileOrErr{}
			return
		}
		file := files.Index(0)
		fr := js.Global().Get("FileReader").New()
		fr.Call("readAsArrayBuffer", file)
		fr.Set("onload", js.NewCallback(func(args2 []js.Value) {
			arrayBuffer := fr.Get("result")
			data := js.Global().Get("Uint8Array").New(arrayBuffer)
			dst := make([]byte, data.Get("length").Int())
			for i := 0; i < len(dst); i++ {
				dst[i] = byte(data.Index(i).Int())
			}
			res <- fileOrErr{
				data: dst,
			}
			return
		}))
		fr.Set("onerror", js.NewCallback(func(args2 []js.Value) {
			res <- fileOrErr{
				err: js.Error{Value: fr.Get("error")},
			}
			return
		}))
	})
	doc := js.Global().Get("document")
	elem := doc.Call("getElementById", elementId)
	elem.Call("addEventListener", "change", cb)
	return res
}

func setImage(data []byte) {
	doc := js.Global().Get("document")
	elem := doc.Call("getElementById", "muxxed")
	if len(data) != 0 {
		enc := base64.StdEncoding
		b64Data := make([]byte, enc.EncodedLen(len(data)))
		enc.Encode(b64Data, data)
		elem.Set("src", "data:image/png;base64,"+string(b64Data))
	} else {
		elem.Set("src", noImageData)
	}
}

func check(thumb, full []byte) ([]byte, error) {
	if thumb == nil || full == nil {
		return nil, nil
	}
	dst := new(bytes.Buffer)
	t := bytes.NewBuffer(thumb)
	f := bytes.NewBuffer(full)
	if err := internal.GammaMuxData(t, f, dst /*dither=*/, true /*stretch=*/, true); err != nil {
		return nil, err
	}
	return dst.Bytes(), nil
}

func main() {
	thumbFile := watchFile("thumb")
	fullFile := watchFile("full")
	console := js.Global().Get("console")

	var thumb []byte
	var full []byte
	for {
		select {
		case r := <-thumbFile:
			if r.err != nil {
				console.Call("error", r.err)
			} else {
				thumb = r.data
			}
		case r := <-fullFile:
			if r.err != nil {
				console.Call("error", r.err)
			} else {
				full = r.data
			}
		}
		setImage(nil)
		dst, err := check(thumb, full)
		if err != nil {
			console.Call("error", err.Error())
		} else {
			setImage(dst)
		}
	}
}
