package main

import (
	"bytes"
	"encoding/base64"
	"syscall/js"

	"github.com/carl-mastrangelo/gammux/internal"
)

const noImageData = "data:image/gif;base64,R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs="

type fileOrErr struct {
	data []byte
	err  error
}

func watchFile(elementId string) <-chan fileOrErr {
	res := make(chan fileOrErr)
	cb := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		files := args[0].Get("target").Get("files")
		if files.Get("length").Int() != 1 {
			res <- fileOrErr{}
			return nil
		}
		file := files.Index(0)
		fr := js.Global().Get("FileReader").New()
		fr.Call("readAsArrayBuffer", file)
		fr.Set("onload", js.FuncOf(func(_ js.Value, args2 []js.Value) interface{} {
			arrayBuffer := fr.Get("result")
			data := js.Global().Get("Uint8Array").New(arrayBuffer)
			dst := make([]byte, data.Get("length").Int())
			for i := 0; i < len(dst); i++ {
				dst[i] = byte(data.Index(i).Int())
			}
			res <- fileOrErr{
				data: dst,
			}
			return nil
		}))
		fr.Set("onerror", js.FuncOf(func(_ js.Value, args2 []js.Value) interface{} {
			res <- fileOrErr{
				err: js.Error{Value: fr.Get("error")},
			}
			return nil
		}))
		return nil
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

func gen(thumb, full []byte) ([]byte, error) {
	dst := new(bytes.Buffer)
	t := bytes.NewBuffer(thumb)
	f := bytes.NewBuffer(full)
	if err := internal.GammaMuxData(t, f, dst /*dither=*/, true /*stretch=*/, true); err != nil {
		return nil, err
	}
	return dst.Bytes(), nil
}

func publishError(msg string) {
	console := js.Global().Get("console")
	console.Call("error", msg)
	doc := js.Global().Get("document")
	elem := doc.Call("getElementById", "error")
	elem.Set("innerText", msg)
	elem.Set("className", "errorText")
}

func publishNotice(msg string) {
	doc := js.Global().Get("document")
	elem := doc.Call("getElementById", "error")
	elem.Set("innerText", msg)
	elem.Set("className", "noticeText")
}

func main() {
	thumbFile := watchFile("thumb")
	fullFile := watchFile("full")

	var thumb []byte
	var full []byte
	for {
		select {
		case r := <-thumbFile:
			if r.err != nil {
				publishError(r.err.Error())
			} else {
				thumb = r.data
			}
		case r := <-fullFile:
			if r.err != nil {
				publishError(r.err.Error())
			} else {
				full = r.data
			}
		}
		setImage(nil)
		if len(thumb) == 0 || len(full) == 0 {
			continue
		}
		publishNotice("Working...")
		js.Global().Get("setTimeout").Invoke(js.FuncOf(func(_ js.Value, _ []js.Value) interface{} {
			if dst, err := gen(thumb, full); err != nil {
				publishError(err.Error())
			} else {
				setImage(dst)
				publishNotice("")
			}
			return nil
		}), 0)
	}
}
