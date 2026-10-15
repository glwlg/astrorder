import { test } from 'node:test'
import assert from 'node:assert/strict'
import { installFilePaths, resolvePreviewImages } from './desktop-files.js'

test('file objects are correlated independently even with identical names', async () => {
  const sent=[]
  const win={setTimeout,clearTimeout,_wails:{handlePlatformFileDrop(){}},chrome:{webview:{postMessageWithAdditionalObjects:(message,files)=>sent.push({message,files})}}}
  const getPath=installFilePaths(win)
  const a={name:'same.txt'}, b={name:'same.txt'}
  const pa=getPath(a),pb=getPath(b)
  assert.equal(getPath(a),pa)
  assert.equal(sent.length,1)
  win._wails.handlePlatformFileDrop(['C:/中文;a/same.txt'],-1,-987654)
  assert.equal(await pa,'C:/中文;a/same.txt')
  assert.equal(sent.length,2)
  win._wails.handlePlatformFileDrop(['C:/b/same.txt'],-2,-987654)
  assert.equal(await pb,'C:/b/same.txt')
})
test('clipboard image with no path remains pathless',async()=>{
 const win={setTimeout,clearTimeout,_wails:{},chrome:{webview:{postMessageWithAdditionalObjects:()=>queueMicrotask(()=>win._wails.handlePlatformFileDrop([''],-1,-987654))}}}
 assert.equal(await installFilePaths(win)({name:'image.png'}),'')
})
test('memory-backed files rejected by WebView2 remain pathless', async () => {
 const win={setTimeout,clearTimeout,_wails:{},chrome:{webview:{postMessageWithAdditionalObjects:()=>{throw new TypeError('additional File object is not a file on the disk.')}}}}
 assert.equal(await installFilePaths(win)({name:'clipboard.png'}),'')
})
test('ordinary browser has no native path',async()=>assert.equal(await installFilePaths({})({name:'a'}),''))
test('preview refuses remote images before fetch',async()=>{
 const win={location:{origin:'http://wails.localhost'},fetch:()=>{throw new Error('must not fetch')}}
 await assert.rejects(resolvePreviewImages(['http://evil.test/a.png'],win),/跨来源/)
 await assert.rejects(resolvePreviewImages(['file:///C:/a.png'],win),/跨来源/)
})
