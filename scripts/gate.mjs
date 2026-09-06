import { execFileSync } from 'node:child_process'
import { mkdirSync, readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

const root = new URL('..', import.meta.url).pathname.replace(/^\/(.:\/)/, '$1')

function run(command, args) {
  process.stdout.write(`> ${command} ${args.join(' ')}\n`)
  execFileSync(command, args, { cwd: root, stdio: 'inherit' })
}

function output(command, args) {
  return execFileSync(command, args, { cwd: root, encoding: 'utf8' }).trim()
}

function goFiles(directory) {
  const files = []
  for (const entry of readdirSync(directory)) {
    if (['.cache', '.git', 'new-api', 'node_modules', '.pnpm-store', 'dist'].includes(entry)) continue
    const path = join(directory, entry)
    const stat = statSync(path)
    if (stat.isDirectory()) files.push(...goFiles(path))
    else if (entry.endsWith('.go')) files.push(path)
  }
  return files
}

const files = goFiles(root)
const unformatted = output('gofmt', ['-l', ...files])
if (unformatted) {
  throw new Error(`Go files require formatting:\n${unformatted}`)
}
const importIssues = output('go', [
  'run',
  'golang.org/x/tools/cmd/goimports@v0.38.0',
  '-l',
  ...files,
])
if (importIssues) {
  throw new Error(`Go imports require formatting:\n${importIssues}`)
}

const generatedFiles = [
  ...goFiles(join(root, 'internal/transport/http/apispec')),
  ...goFiles(join(root, 'internal/adapters/storage/postgres/sqlc')),
]
const generatedBefore = new Map(
  generatedFiles.map((file) => [file, readFileSync(file, 'utf8')])
)
run('go', ['generate', './api', './db'])
for (const file of generatedFiles) {
  if (generatedBefore.get(file) !== readFileSync(file, 'utf8')) {
    throw new Error(`Generated file is stale: ${file}`)
  }
}

const testBuildTags = 'integration,contract'
run('go', ['vet', `-tags=${testBuildTags}`, './...'])
const packages = output('go', ['list', `-tags=${testBuildTags}`, './...'])
  .split(/\r?\n/)
  .filter(
    (name) =>
      name &&
      !name.endsWith('/internal/transport/http/apispec') &&
      !name.endsWith('/internal/adapters/storage/postgres/sqlc')
  )
run('go', [
  'run',
  'honnef.co/go/tools/cmd/staticcheck@v0.7.0',
  `-tags=${testBuildTags}`,
  ...packages,
])
run('go', [
  'run',
  'github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2',
  'run',
  `--build-tags=${testBuildTags}`,
  './...',
])
run('go', ['test', './...'])
run('go', ['test', '-tags=integration', './internal/adapters/storage/postgres'])
run('go', ['test', '-tags=contract', './compatibility'])
mkdirSync(join(root, 'bin'), { recursive: true })
run('go', ['build', '-o', 'bin/manyrouter.exe', './cmd/manyrouter'])

function pnpm(args) {
  if (process.platform === 'win32') run('cmd.exe', ['/d', '/s', '/c', 'pnpm', ...args])
  else run('pnpm', args)
}
const frontendTypes = join(root, 'web/src/api/operations.gen.ts')
const frontendTypesBefore = readFileSync(frontendTypes, 'utf8')
pnpm(['--dir', 'web', 'generate'])
pnpm(['--dir', 'web', 'exec', 'prettier', '--write', 'src/api/operations.gen.ts'])
if (frontendTypesBefore !== readFileSync(frontendTypes, 'utf8')) throw new Error('Generated frontend API types are stale')
pnpm(['--dir', 'web', 'check'])
pnpm(['--dir', 'web', 'test'])
pnpm(['--dir', 'web', 'format:check'])
pnpm(['--dir', 'web', 'build'])

const roadmap = readFileSync(join(root, 'docs/roadmap.md'), 'utf8')
for (const moduleDocument of [
  '站点与供应商接入.md',
  '单站线路方案.md',
  'New API同步与核对.md',
  '人工策略管理.md',
  '稳定价格管理.md',
  '运营账号与权限.md',
  '运营控制台.md',
]) {
  const contents = readFileSync(join(root, 'docs/模块设计', moduleDocument), 'utf8')
  if (!roadmap.includes(moduleDocument.replace(' ', '%20')) || !contents.includes('../roadmap.md#m1')) {
    throw new Error(`M1 document link is incomplete: ${moduleDocument}`)
  }
}

for (const moduleDocument of [
  '真实流量采集与测量事实.md',
  '主动测评与模型真实性.md',
  '窗口统计与影子评分.md',
]) {
  const contents = readFileSync(join(root, 'docs/模块设计', moduleDocument), 'utf8')
  if (!roadmap.includes(moduleDocument) || !contents.includes('../roadmap.md#m2')) {
    throw new Error(`M2 document link is incomplete: ${moduleDocument}`)
  }
}

for (const moduleDocument of [
  '自动线路发布与故障恢复.md',
  '用户产品数据与 New API 页面.md',
]) {
  const contents = readFileSync(join(root, 'docs/模块设计', moduleDocument), 'utf8')
  const roadmapLink = moduleDocument.replaceAll(' ', '%20')
  if (!roadmap.includes(roadmapLink) || !contents.includes('../roadmap.md#m3')) {
    throw new Error(`M3 document link is incomplete: ${moduleDocument}`)
  }
}
