import type { ComponentType, SVGProps } from 'react'
import {
  File, FileText, FileImage, FileVideo, FileAudio, FileArchive,
  FileJson, FileCode, FileCog, FileTerminal, FileKey, FileType,
  FileDiff, FileSignature, Database, BookOpen, BookText, Mail,
  Calendar, Contact, HardDrive, Box, Palette, Aperture, Map,
  BarChart3, Sigma, Notebook, Captions, Music, Workflow, Magnet,
  History, Hourglass, Lock,
} from 'lucide-react'
import {
  BsFiletypePdf, BsFiletypeDocx, BsFiletypeXlsx, BsFiletypePptx,
} from 'react-icons/bs'
import { cn } from './utils'

export type FileIconKind =
  | 'image'
  | 'raw'
  | 'design'
  | 'pdf'
  | 'word'
  | 'richtext'
  | 'excel'
  | 'ppt'
  | 'archive'
  | 'executable'
  | 'shell'
  | 'audio'
  | 'video'
  | 'midi'
  | 'subtitle'
  | 'js'
  | 'ts'
  | 'json'
  | 'html'
  | 'xml'
  | 'yaml'
  | 'markdown'
  | 'css'
  | 'code'
  | 'latex'
  | 'text'
  | 'config'
  | 'database'
  | 'data'
  | 'cert'
  | 'signature'
  | 'font'
  | 'ebook'
  | 'mail'
  | 'calendar'
  | 'contact'
  | 'disk'
  | 'diff'
  | 'cad3d'
  | 'gis'
  | 'notebook'
  | 'bibliography'
  | 'diagram'
  | 'torrent'
  | 'backup'
  | 'tmp'
  | 'lock'
  | 'unknown'

const EXT_MAP: Record<string, FileIconKind> = {
  // image
  jpg: 'image', jpeg: 'image', png: 'image', gif: 'image', webp: 'image',
  bmp: 'image', svg: 'image', ico: 'image', avif: 'image', heic: 'image', tiff: 'image',
  // raw photo
  dng: 'raw', raw: 'raw', cr2: 'raw', cr3: 'raw', nef: 'raw', arw: 'raw',
  raf: 'raw', orf: 'raw', rw2: 'raw',
  // design
  psd: 'design', ai: 'design', xd: 'design', fig: 'design', sketch: 'design',
  afdesign: 'design', afphoto: 'design', afpub: 'design', eps: 'design',
  cdr: 'design', indd: 'design', idml: 'design', odg: 'design',
  // pdf + document-like
  pdf: 'pdf', xps: 'pdf', oxps: 'pdf', djvu: 'pdf', ps: 'pdf',
  // office word
  doc: 'word', docx: 'word', odt: 'word', pages: 'word',
  dot: 'word', dotx: 'word', ott: 'word',
  docm: 'word', dotm: 'word', fodt: 'word',
  sxw: 'word', stw: 'word', wps: 'word', wpt: 'word',
  rtf: 'richtext',
  // office excel
  xls: 'excel', xlsx: 'excel', ods: 'excel', csv: 'excel', tsv: 'excel',
  numbers: 'excel', xlt: 'excel', xltx: 'excel', ots: 'excel',
  odc: 'excel', odf: 'excel',
  xlsm: 'excel', xltm: 'excel', xlsb: 'excel', fods: 'excel',
  sxc: 'excel', et: 'excel', ett: 'excel',
  // office ppt
  ppt: 'ppt', pptx: 'ppt', odp: 'ppt', key: 'ppt', keynote: 'ppt',
  pot: 'ppt', potx: 'ppt', otp: 'ppt',
  pps: 'ppt', ppsx: 'ppt', pptm: 'ppt', potm: 'ppt', ppsm: 'ppt',
  fodp: 'ppt', sxi: 'ppt', dps: 'ppt', dpt: 'ppt',
  // archive
  zip: 'archive', '7z': 'archive', rar: 'archive', tar: 'archive',
  gz: 'archive', bz2: 'archive', xz: 'archive', zst: 'archive',
  tgz: 'archive', tbz2: 'archive',
  // exec / shell / mobile / bytecode
  exe: 'executable', msi: 'executable', app: 'executable', appimage: 'executable',
  deb: 'executable', rpm: 'executable',
  apk: 'executable', ipa: 'executable', aab: 'executable', xcarchive: 'executable',
  wasm: 'executable',
  sh: 'shell', cmd: 'shell', bat: 'shell', ps1: 'shell',
  // audio
  mp3: 'audio', wav: 'audio', flac: 'audio', ogg: 'audio', m4a: 'audio',
  aac: 'audio', opus: 'audio',
  aif: 'audio', aiff: 'audio', wma: 'audio', alac: 'audio',
  als: 'audio', flp: 'audio',
  mid: 'midi', midi: 'midi',
  // video
  mp4: 'video', mkv: 'video', mov: 'video', avi: 'video', webm: 'video',
  wmv: 'video', flv: 'video',
  mxf: 'video', m2ts: 'video', vob: 'video', m3u8: 'video',
  prproj: 'video', aep: 'video',
  // subtitles
  srt: 'subtitle', vtt: 'subtitle', ass: 'subtitle', sub: 'subtitle',
  // code
  js: 'js', jsx: 'js', mjs: 'js', cjs: 'js',
  ts: 'ts', tsx: 'ts',
  json: 'json', jsonc: 'json',
  html: 'html', htm: 'html',
  xml: 'xml', xsd: 'xml',
  yml: 'yaml', yaml: 'yaml',
  md: 'markdown', markdown: 'markdown',
  css: 'css', scss: 'css', sass: 'css', less: 'css',
  py: 'code', go: 'code', rs: 'code', java: 'code', c: 'code', cpp: 'code',
  cc: 'code', h: 'code', hpp: 'code', cs: 'code', rb: 'code', php: 'code',
  swift: 'code', kt: 'code', lua: 'code', r: 'code', dart: 'code',
  // additional code
  vue: 'code', svelte: 'code', astro: 'code', mdx: 'code',
  pug: 'code', jade: 'code', ejs: 'code', hbs: 'code',
  handlebars: 'code', liquid: 'code', njk: 'code',
  graphql: 'code', gql: 'code', proto: 'code', thrift: 'code',
  glsl: 'code', hlsl: 'code', wgsl: 'code', frag: 'code', vert: 'code', comp: 'code',
  clj: 'code', cljs: 'code', ex: 'code', exs: 'code', erl: 'code', hrl: 'code',
  hs: 'code', ml: 'code', mli: 'code', fs: 'code', fsx: 'code',
  nim: 'code', zig: 'code', v: 'code', cr: 'code', jl: 'code',
  pl: 'code', pm: 'code', groovy: 'code', gradle: 'code', scala: 'code', sbt: 'code',
  asm: 'code', s: 'code', vb: 'code', vbs: 'code', pas: 'code',
  f90: 'code', f95: 'code', cob: 'code', ada: 'code', tcl: 'code',
  lisp: 'code', scm: 'code', rkt: 'code',
  map: 'code', coffee: 'code',
  // latex
  tex: 'latex', sty: 'latex', cls: 'latex',
  // text
  txt: 'text', log: 'text',
  // config / IaC / system
  ini: 'config', conf: 'config', cfg: 'config', toml: 'config',
  env: 'config', properties: 'config',
  tf: 'config', tfvars: 'config', tfstate: 'config', hcl: 'config',
  nomad: 'config', nix: 'config',
  reg: 'config', plist: 'config', desktop: 'config',
  // db & data
  sql: 'database', db: 'database', sqlite: 'database', sqlite3: 'database',
  parquet: 'data', arrow: 'data', feather: 'data', avro: 'data', orc: 'data',
  hdf5: 'data', h5: 'data', nc: 'data', mat: 'data', rds: 'data', rdata: 'data',
  npy: 'data', npz: 'data', pkl: 'data', pickle: 'data',
  // cert / signature
  pem: 'cert', crt: 'cert', cer: 'cert', pfx: 'cert', p12: 'cert',
  kdbx: 'cert', pub: 'cert', '1pux': 'cert',
  gpg: 'signature', asc: 'signature', sig: 'signature', sigstore: 'signature',
  sha256: 'signature', md5: 'signature',
  // font
  ttf: 'font', otf: 'font', woff: 'font', woff2: 'font', eot: 'font',
  // ebook
  epub: 'ebook', mobi: 'ebook', azw3: 'ebook',
  // mail / cal / contact
  eml: 'mail', msg: 'mail', mbox: 'mail',
  ics: 'calendar', vcs: 'calendar',
  vcf: 'contact',
  // disk
  iso: 'disk', img: 'disk', vhd: 'disk', vmdk: 'disk', dmg: 'disk',
  // diff
  patch: 'diff', diff: 'diff',
  // 3D / CAD
  stl: 'cad3d', amf: 'cad3d', '3mf': 'cad3d', obj: 'cad3d',
  step: 'cad3d', stp: 'cad3d', gcode: 'cad3d',
  blend: 'cad3d', fbx: 'cad3d', dae: 'cad3d', glb: 'cad3d', gltf: 'cad3d',
  usdz: 'cad3d', ply: 'cad3d', x3d: 'cad3d',
  dwg: 'cad3d', dxf: 'cad3d', iges: 'cad3d', igs: 'cad3d',
  sldprt: 'cad3d', sldasm: 'cad3d',
  // GIS
  geojson: 'gis', kml: 'gis', kmz: 'gis', gpx: 'gis', shp: 'gis',
  gpkg: 'gis', mbtiles: 'gis', osm: 'gis',
  // notebook
  ipynb: 'notebook', rmd: 'notebook', qmd: 'notebook',
  one: 'notebook', onepkg: 'notebook',
  // bibliography
  bib: 'bibliography', ris: 'bibliography', enw: 'bibliography',
  // diagram
  vsd: 'diagram', vsdx: 'diagram', drawio: 'diagram',
  // torrent
  torrent: 'torrent',
  // backup
  bak: 'backup', old: 'backup', orig: 'backup', swp: 'backup',
  // tmp
  tmp: 'tmp', temp: 'tmp', crdownload: 'tmp', part: 'tmp', download: 'tmp',
  // lock
  lock: 'lock',
}

const SPECIAL_NAMES: Record<string, FileIconKind> = {
  dockerfile: 'config',
  containerfile: 'config',
  jenkinsfile: 'config',
  vagrantfile: 'config',
  makefile: 'config',
  gemfile: 'config',
  procfile: 'config',
  brewfile: 'config',
  justfile: 'config',
  rakefile: 'code',
  '.dockerignore': 'config',
  '.gitignore': 'config',
  '.gitattributes': 'config',
  'go.sum': 'lock',
  readme: 'text',
}

function isLockFile(lower: string): boolean {
  return (
    lower.endsWith('-lock.json') ||
    lower.endsWith('-lock.yaml') ||
    lower.endsWith('-lock.yml')
  )
}

function extOf(lower: string): string {
  if (lower.endsWith('.tar.gz') || lower.endsWith('.tar.bz2') ||
      lower.endsWith('.tar.xz') || lower.endsWith('.tar.zst')) {
    return 'tar'
  }
  const i = lower.lastIndexOf('.')
  if (i <= 0) return ''
  return lower.slice(i + 1)
}

export function getFileIconKind(name: string): FileIconKind {
  const lower = name.toLowerCase()
  if (SPECIAL_NAMES[lower]) return SPECIAL_NAMES[lower]
  if (lower.endsWith('~')) return 'backup'
  if (isLockFile(lower)) return 'lock'
  if (lower.startsWith('.env')) return 'config'
  return EXT_MAP[extOf(lower)] ?? 'unknown'
}

// Common SVG props shape so lucide (LucideIcon) and react-icons (IconType)
// can share a single map. Both ultimately render an <svg> and accept className
// and data-* attributes; their declared prop types differ but are compatible
// for our usage.
type IconCmp = ComponentType<SVGProps<SVGSVGElement> & { 'data-file-kind'?: string }>

const KIND_META: Record<FileIconKind, { Icon: IconCmp; color: string }> = {
  image:        { Icon: FileImage as IconCmp,       color: 'text-violet-500' },
  raw:          { Icon: Aperture as IconCmp,        color: 'text-amber-400' },
  design:       { Icon: Palette as IconCmp,         color: 'text-fuchsia-500' },
  pdf:          { Icon: BsFiletypePdf as IconCmp,   color: 'text-red-600' },
  word:         { Icon: BsFiletypeDocx as IconCmp,  color: 'text-blue-600' },
  richtext:     { Icon: FileText as IconCmp,        color: 'text-blue-500' },
  excel:        { Icon: BsFiletypeXlsx as IconCmp,  color: 'text-green-600' },
  ppt:          { Icon: BsFiletypePptx as IconCmp,  color: 'text-orange-600' },
  archive:      { Icon: FileArchive as IconCmp,     color: 'text-yellow-600' },
  executable:   { Icon: FileCog as IconCmp,         color: 'text-muted-foreground' },
  shell:        { Icon: FileTerminal as IconCmp,    color: 'text-muted-foreground' },
  audio:        { Icon: FileAudio as IconCmp,       color: 'text-pink-500' },
  video:        { Icon: FileVideo as IconCmp,       color: 'text-red-500' },
  midi:         { Icon: Music as IconCmp,           color: 'text-pink-400' },
  subtitle:     { Icon: Captions as IconCmp,        color: 'text-sky-400' },
  js:           { Icon: FileCode as IconCmp,        color: 'text-yellow-500' },
  ts:           { Icon: FileCode as IconCmp,        color: 'text-blue-500' },
  json:         { Icon: FileJson as IconCmp,        color: 'text-amber-600' },
  html:         { Icon: FileCode as IconCmp,        color: 'text-orange-500' },
  xml:          { Icon: FileCode as IconCmp,        color: 'text-orange-400' },
  yaml:         { Icon: FileCode as IconCmp,        color: 'text-rose-500' },
  markdown:     { Icon: FileText as IconCmp,        color: 'text-foreground/80' },
  css:          { Icon: FileCode as IconCmp,        color: 'text-sky-500' },
  code:         { Icon: FileCode as IconCmp,        color: 'text-violet-500' },
  latex:        { Icon: Sigma as IconCmp,           color: 'text-foreground/80' },
  text:         { Icon: FileText as IconCmp,        color: 'text-muted-foreground' },
  config:       { Icon: FileCog as IconCmp,         color: 'text-muted-foreground' },
  database:     { Icon: Database as IconCmp,        color: 'text-amber-600' },
  data:         { Icon: BarChart3 as IconCmp,       color: 'text-cyan-600' },
  cert:         { Icon: FileKey as IconCmp,         color: 'text-yellow-600' },
  signature:    { Icon: FileSignature as IconCmp,   color: 'text-yellow-700' },
  font:         { Icon: FileType as IconCmp,        color: 'text-muted-foreground' },
  ebook:        { Icon: BookOpen as IconCmp,        color: 'text-amber-700' },
  mail:         { Icon: Mail as IconCmp,            color: 'text-blue-500' },
  calendar:     { Icon: Calendar as IconCmp,        color: 'text-blue-500' },
  contact:      { Icon: Contact as IconCmp,         color: 'text-blue-500' },
  disk:         { Icon: HardDrive as IconCmp,       color: 'text-muted-foreground' },
  diff:         { Icon: FileDiff as IconCmp,        color: 'text-green-600' },
  cad3d:        { Icon: Box as IconCmp,             color: 'text-cyan-500' },
  gis:          { Icon: Map as IconCmp,             color: 'text-emerald-500' },
  notebook:     { Icon: Notebook as IconCmp,        color: 'text-indigo-500' },
  bibliography: { Icon: BookText as IconCmp,        color: 'text-amber-700' },
  diagram:      { Icon: Workflow as IconCmp,        color: 'text-violet-500' },
  torrent:      { Icon: Magnet as IconCmp,          color: 'text-rose-500' },
  backup:       { Icon: History as IconCmp,         color: 'text-muted-foreground/70' },
  tmp:          { Icon: Hourglass as IconCmp,       color: 'text-muted-foreground/70' },
  lock:         { Icon: Lock as IconCmp,            color: 'text-muted-foreground' },
  unknown:      { Icon: File as IconCmp,            color: 'text-muted-foreground' },
}

export interface FileIconProps {
  name: string
  className?: string
}

export function FileIcon({ name, className }: FileIconProps) {
  const kind = getFileIconKind(name)
  const { Icon, color } = KIND_META[kind]
  return <Icon data-file-kind={kind} className={cn(color, 'shrink-0', className)} />
}
