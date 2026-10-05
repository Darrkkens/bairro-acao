// Phones produce 3–8 MB photos. Re-encoding to ~1600 px keeps uploads quick on
// weak signal, is plenty for the model (Gemma 3 sees 896×896) and drops EXIF,
// including the GPS tag, so only the location the person chose is shared.
const MAX_SIDE = 1600
const QUALITY = 0.82

interface Decoded {
  image: CanvasImageSource
  width: number
  height: number
  close(): void
}

async function decode(file: Blob): Promise<Decoded> {
  try {
    const bitmap = await createImageBitmap(file, { imageOrientation: 'from-image' })
    return { image: bitmap, width: bitmap.width, height: bitmap.height, close: () => bitmap.close() }
  } catch {
    const url = URL.createObjectURL(file)
    try {
      const img = new Image()
      img.src = url
      await img.decode()
      return { image: img, width: img.naturalWidth, height: img.naturalHeight, close: () => {} }
    } finally {
      URL.revokeObjectURL(url)
    }
  }
}

export async function preparePhoto(file: Blob): Promise<Blob> {
  const source = await decode(file)
  try {
    const scale = Math.min(1, MAX_SIDE / Math.max(source.width, source.height))
    const canvas = document.createElement('canvas')
    canvas.width = Math.round(source.width * scale)
    canvas.height = Math.round(source.height * scale)
    const ctx = canvas.getContext('2d')
    if (!ctx) throw new Error('canvas indisponível')
    ctx.drawImage(source.image, 0, 0, canvas.width, canvas.height)
    return await new Promise<Blob>((resolve, reject) =>
      canvas.toBlob((blob) => (blob ? resolve(blob) : reject(new Error('Não foi possível processar a foto.'))), 'image/jpeg', QUALITY),
    )
  } finally {
    source.close()
  }
}
