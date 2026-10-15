"""Re-encode the images of a PDF, and nothing else.

Usage: python3 -I - SRC DST DPI   (the script arrives on stdin)

Content streams and fonts are left byte for byte as they were, so the text a
reader extracts is the text it extracted before; Ghostscript, which rewrites
the fonts, lost figure labels and split ligatures on this library. An image is
resampled to at most DPI over the largest page of the document, which bounds
what any placement of it can show, and stored as a JPEG; one that would not get
smaller is left alone.
"""

import io
import sys
import zlib
from collections import Counter

import pikepdf
from pikepdf import Name, PdfImage
from PIL import Image

QUALITY = 75


def page_limits(pdf, dpi):
    w = h = 0.0
    for page in pdf.pages:
        box = page.mediabox
        w = max(w, abs(float(box[2]) - float(box[0])))
        h = max(h, abs(float(box[3]) - float(box[1])))
    return int(w / 72 * dpi), int(h / 72 * dpi)


def resized(img, limit):
    # An image may be placed rotated, so either side may span the longer page side.
    side = max(limit)
    scale = min(1.0, side / img.width, side / img.height)
    if scale >= 1.0:
        return img
    return img.resize((max(1, round(img.width * scale)), max(1, round(img.height * scale))), Image.LANCZOS)


def plain(stream):
    for key in ('/DecodeParms', '/Decode'):
        if key in stream:
            del stream[key]


def shrink_image(raw, limit, done):
    # Stencil masks and colour-key masks are bit-exact; JPEG would blur them.
    if raw.get('/ImageMask', False) or raw.get('/Mask') is not None:
        return False
    smask = raw.get('/SMask')
    if smask is not None and smask.get('/Matte') is not None:
        return False
    try:
        img = PdfImage(raw).as_pil_image()
        # Pillow reads the data only when it is first used; a stream that breaks
        # off has to fail here, where it costs this image, not the whole file.
        img.load()
    except Exception:
        return False
    # Bilevel pages of a scan are smaller as they are (CCITT, JBIG2) than as JPEG,
    # and CMYK has no faithful plain conversion.
    if img.mode not in ('RGB', 'L', 'P'):
        return False
    keep_space = img.mode in ('RGB', 'L') and raw.get('/ColorSpace') is not None \
        and not str(raw.ColorSpace).startswith('[/Indexed')
    if img.mode == 'P':
        img = img.convert('RGB')
    img = resized(img, limit)

    buf = io.BytesIO()
    img.save(buf, 'JPEG', quality=QUALITY, optimize=True)
    data = buf.getvalue()
    if len(data) >= len(raw.read_raw_bytes()):
        return False

    if smask is not None:
        try:
            mask = PdfImage(smask).as_pil_image()
            mask.load()
            mask = mask.convert('L')
        except Exception:
            return False
        if mask.size != img.size:
            mask = mask.resize(img.size, Image.LANCZOS)
        smask.write(zlib.compress(mask.tobytes(), 9), filter=Name.FlateDecode)
        smask.Width, smask.Height = mask.width, mask.height
        smask.ColorSpace = Name.DeviceGray
        smask.BitsPerComponent = 8
        plain(smask)
        done.add(smask.objgen)

    colorspace = raw.ColorSpace if keep_space else None
    raw.write(data, filter=Name.DCTDecode)
    plain(raw)
    raw.Width, raw.Height = img.width, img.height
    raw.BitsPerComponent = 8
    raw.ColorSpace = colorspace if colorspace is not None else (Name.DeviceGray if img.mode == 'L' else Name.DeviceRGB)
    return True


def main(src, dst, dpi):
    with pikepdf.open(src) as pdf:
        limit = page_limits(pdf, dpi)
        images = [o for o in pdf.objects if isinstance(o, pikepdf.Stream) and o.get('/Subtype') == Name.Image]
        # A soft mask is an image too, but it is resized with the image it masks.
        masks = Counter(o.SMask.objgen for o in images if o.get('/SMask') is not None)
        done = set(masks)
        # A mask shared by two images cannot be resized to fit both.
        done.update(o.objgen for o in images if o.get('/SMask') is not None and masks[o.SMask.objgen] > 1)
        for obj in images:
            if obj.objgen in done:
                continue
            done.add(obj.objgen)
            shrink_image(obj, limit, done)
        pdf.save(dst, compress_streams=True, object_stream_mode=pikepdf.ObjectStreamMode.generate)


if __name__ == '__main__':
    main(sys.argv[1], sys.argv[2], int(sys.argv[3]))
