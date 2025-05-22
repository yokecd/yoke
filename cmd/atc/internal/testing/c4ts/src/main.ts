import * as fs from 'node:fs'
import * as path from 'node:path'
import * as http from 'node:http'

const images = path.join(__dirname, 'imgs')

const cats = fs
  .readdirSync(images, { withFileTypes: true })
  .filter(file => !file.isDirectory())
  .map(file => fs.readFileSync(path.join(images, file.name)))

let i = 0

const server = http.createServer((_, resp) => {
  const cat = cats[i++ % cats.length]

  resp
    .writeHead(200, {
      'Content-Type': 'image/jpeg',
      'Content-Length': cat.length,
    })
    .end(cat)
})

const port = Number(process.env['PORT']) || 3000

server
  .listen(port)
  .on('listening', () => console.log(`listening on port ${port}`))
  .on('error', err => {
    console.error(`error starting server: ${err.message}`)
    process.exit(1)
  })

await Promise.any([
  new Promise(resolve => process.on('SIGINT', resolve)),
  new Promise(resolve => process.on('SIGTERM', resolve)),
])

server.close(() => process.exit(0))
