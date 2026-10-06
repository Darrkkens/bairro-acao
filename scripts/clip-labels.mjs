// Embeds the photo gate's descriptions with CLIP's text encoder, writing
// backend/internal/vision/labels.json. The vision side (Go) uses the same
// checkpoint, so text and photo vectors live in one space.
//
//   mkdir -p /tmp/clip && cd /tmp/clip && npm init -y && npm i @huggingface/transformers@3 --ignore-scripts
//   node --experimental-default-type=module <repo>/scripts/clip-labels.mjs <repo>/backend/internal/vision/labels.json
//   (run from /tmp/clip so the package resolves)
import { AutoTokenizer, CLIPTextModelWithProjection } from '@huggingface/transformers'
import { writeFileSync } from 'node:fs'

const MODEL = 'Xenova/clip-vit-base-patch32' // ONNX export of openai/clip-vit-base-patch32 (MIT)

// [app category or "fora", English text for CLIP, Portuguese shown to the person]
const labels = [
  ['limpeza', 'a photo of garbage and trash bags dumped on a sidewalk', 'lixo na calçada'],
  ['limpeza', 'a photo of litter and rubbish on the street', 'lixo na rua'],
  ['limpeza', 'a photo of illegal dumping on a vacant lot', 'descarte irregular'],
  ['limpeza', 'a photo of an overflowing trash bin on the street', 'lixeira transbordando'],
  ['limpeza', 'a photo of discarded furniture and a mattress left on the curb', 'móveis descartados'],
  ['limpeza', 'a photo of construction debris and rubble dumped on the street', 'entulho'],
  ['outros', 'a photo of graffiti vandalism on a wall', 'pichação'],
  ['via_publica', 'a photo of a damaged or bent traffic sign', 'placa danificada'],
  ['via_publica', 'a photo of a faded or rusty street sign', 'placa desbotada'],
  ['via_publica', 'a photo of a pothole in an asphalt road', 'buraco no asfalto'],
  ['via_publica', 'a photo of a cracked asphalt road surface', 'asfalto trincado'],
  ['via_publica', 'a photo of a damaged manhole cover or storm drain', 'bueiro danificado'],
  ['via_publica', 'a photo of a broken street light', 'poste ou iluminação'],
  ['calcadas', 'a photo of a broken sidewalk with cracked paving tiles', 'calçada quebrada'],
  ['calcadas', 'a photo of a damaged curb', 'meio-fio danificado'],
  ['calcadas', 'a photo of a sidewalk blocked by an obstacle', 'calçada obstruída'],
  ['lazer', 'a photo of a broken bench in a public park', 'banco quebrado'],
  ['lazer', 'a photo of damaged playground equipment', 'brinquedo danificado'],
  ['outros', 'a photo of overgrown weeds on a public sidewalk', 'mato alto'],
  ['outros', 'a photo of a fallen tree on the street', 'árvore caída'],
  ['fora', 'a photo of a car', 'um carro'],
  ['fora', 'a photo of a house facade', 'a fachada de uma casa'],
  ['fora', 'a portrait photo of a person', 'o retrato de uma pessoa'],
  ['fora', 'a selfie', 'uma selfie'],
  ['fora', 'a group of people at an event', 'um grupo de pessoas'],
  ['fora', 'a photo of food on a plate', 'comida'],
  ['fora', 'a photo of a dog', 'um cachorro'],
  ['fora', 'a photo of a cat', 'um gato'],
  ['fora', 'a screenshot of a phone or computer screen', 'um print de tela'],
  ['fora', 'a default user profile avatar icon', 'um avatar ou ícone'],
  ['fora', 'a photo of a document or book with text', 'um documento ou texto'],
  ['fora', 'a photo of the sky and clouds', 'o céu'],
  ['fora', 'a close-up photo of a flower', 'uma flor'],
  ['fora', 'a photo of a room interior', 'um ambiente interno'],
  ['fora', 'a photo of a laptop on a desk', 'um computador'],
  ['fora', 'a painting or drawing', 'uma pintura ou desenho'],
  ['fora', 'a meme or cartoon', 'um meme ou desenho animado'],
]

const tokenizer = await AutoTokenizer.from_pretrained(MODEL)
const model = await CLIPTextModelWithProjection.from_pretrained(MODEL, { dtype: 'fp32' })
const { text_embeds } = await model(tokenizer(labels.map(([, text]) => text), { padding: true, truncation: true }))
const dims = text_embeds.dims[1]
const out = labels.map(([category, text, pt], i) => {
  const v = Array.from(text_embeds.data.slice(i * dims, (i + 1) * dims))
  const norm = Math.hypot(...v)
  return { category, text, pt, embedding: v.map((x) => Number((x / norm).toFixed(6))) }
})
writeFileSync(process.argv[2], JSON.stringify({ model: `${MODEL} (openai/clip-vit-base-patch32, MIT)`, labels: out }))
console.log(`${out.length} labels × ${dims} → ${process.argv[2]}`)
