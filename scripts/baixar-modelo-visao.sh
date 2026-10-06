#!/usr/bin/env bash
# Baixa o filtro de fotos (cerca de 110 MB) para backend/data/vision:
# - ONNX Runtime 1.29.0 (MIT), que roda o modelo dentro do backend Go;
# - CLIP ViT-B/32 quantizado em ONNX (openai/clip-vit-base-patch32, MIT).
# Sem esses arquivos o app funciona normalmente, só sem descartar fotos fora
# do tema nem agrupar fotos do mesmo ponto antes do Gemma.
set -euo pipefail
cd "$(dirname "$0")/../backend"
DIR="${VISION_DIR:-data/vision}"
mkdir -p "$DIR"

ORT=onnxruntime-linux-x64-1.29.0
if [ ! -f "$DIR/$ORT/lib/libonnxruntime.so" ]; then
  echo "Baixando ONNX Runtime 1.29.0…"
  curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v1.29.0/$ORT.tgz" | tar -xz -C "$DIR"
fi

if [ ! -f "$DIR/clip-vision-q8.onnx" ]; then
  echo "Baixando CLIP ViT-B/32 (89 MB)…"
  curl -fsSL -o "$DIR/clip-vision-q8.onnx.part" "https://huggingface.co/Xenova/clip-vit-base-patch32/resolve/main/onnx/vision_model_quantized.onnx"
  mv "$DIR/clip-vision-q8.onnx.part" "$DIR/clip-vision-q8.onnx"
fi
echo "Filtro de fotos pronto em backend/$DIR"
