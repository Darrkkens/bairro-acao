#!/usr/bin/env bash
# Roda o Bairro em Ação como servidor para celulares na mesma rede Wi-Fi:
# app + API + fotos em https://<ip-do-notebook>:8443, num único processo.
# Na primeira vez, instale no celular o certificado em https://<ip>:8443/ca.crt
# (veja o README, "Usando no celular").
set -euo pipefail
cd "$(dirname "$0")/.."
PORT="${PORT:-8443}"

docker compose up -d postgres >/dev/null
if ! curl -sf http://localhost:11434/api/tags >/dev/null; then
  echo "Iniciando o Ollama…"
  mkdir -p backend/data
  nohup ollama serve >backend/data/ollama.log 2>&1 &
  sleep 3
fi

echo "Gerando o app…"
(cd frontend && npm run build --silent >/dev/null)
(cd backend && go build -o server ./cmd/server)

cd backend
# Variáveis daqui têm prioridade sobre o .env.
API_ADDR="0.0.0.0:${PORT}" WEB_DIR=../frontend/dist TLS=auto exec ./server
