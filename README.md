# Gammux
A Gamma muxing tool

This tool merges two pictures together by splitting them into high 
and low brightness images.   The lighter image is scaled based on a [custom
gamma](http://www.libpng.org/pub/png/spec/1.2/PNG-Chunks.html#C.gAMA]) amount, 
which most programs don't support.   However, browsers typically do support 
gamma, which affords the ability to make an image appear differently based
on where it is viewed.

## Example

To run:

```bash
go run gammux.go -full ./fine.jpg -thumbnail ./notfine.jpg  -dest merged.png
```

or if you want to use the **Python2** version:

```
py -2 gammux.py fine.jpg notfine.jpg merged.png
```

Make sure you have te pillow library installed. If not use this command:

```
py -2 -m pip install pillow
```

The tool takes 2 images as input:

1. The thumbnail, is what will be shown by non compliant implementations.
2. The full image, which will be shown by compliant implementations.

In the example, this is the Full:

![fine.jpg](https://github.com/carl-mastrangelo/gammux/raw/master/fine.jpg "Fine")

And the thumbnail image:

![notfine.jpg](https://github.com/carl-mastrangelo/gammux/raw/master/notfine.jpg "Not Fine")

This will produce 

![merged.png](https://github.com/carl-mastrangelo/gammux/raw/master/merged.png "Merged")


Depending on your browser, or phone, or whatever you use to see this, you will see one of two 
images.

### Compliant

![compliant.png](https://github.com/carl-mastrangelo/gammux/raw/master/compliant.png "Compliant")

### Non Compliant

![noncompliant.png](https://github.com/carl-mastrangelo/gammux/raw/master/noncompliant.png "Non Compliant")





