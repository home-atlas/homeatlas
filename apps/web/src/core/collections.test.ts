// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { registerModules, getWebModule, type ModuleStatus } from './modules'
import { setAuthState } from './auth'
import { get } from './api'
vi.mock('./api',async original=>({...await original<typeof import('./api')>(),get:vi.fn()}))
let unmount:(()=>void)|undefined
const collection={title:'Archivos escolares',description:'Información del colegio',categories:[{value:'escuela',label:'Escuela'}],mediaTypes:['text/plain'],maxFileBytes:1024,titleMaxLength:100,descriptionMaxLength:500,maxTags:3,tagMaxLength:20}
const module:ModuleStatus={id:'documents',name:'Documentos',version:'1',contractVersion:'1',runtime:'wasi-preview1',collection,enabled:true,available:true,accessPermission:'documents.read',navigation:{label:'Documentos',path:'/documents'},capabilities:['documents.read','documents.write','documents.share'],dependencies:[]}
afterEach(()=>{unmount?.();unmount=undefined;document.body.innerHTML='';vi.resetAllMocks();setAuthState({setupRequired:false,session:null})})
it('renders the installed collection declaration rather than the former built-in frontend',async()=>{
 setAuthState({setupRequired:false,session:{user:{id:'owner',name:'Owner',email:'',username:'owner',emailVerified:false},household:{id:'home',name:'Home',timezone:'UTC'},role:'owner',permissions:module.capabilities,expiresAt:'2027-01-01T00:00:00Z'}})
 vi.mocked(get).mockResolvedValue([])
 const router=createRouter({history:createMemoryHistory(),routes:[{path:'/',component:{template:'<p>Inicio</p>'}}]})
 await registerModules(router,[module]);await router.push('/documents')
 const root=document.createElement('div'),app=createApp(RouterView).use(router);document.body.append(root);app.mount(root);unmount=()=>app.unmount()
 await vi.waitFor(()=>expect(root.textContent).toContain('Archivos escolares'))
 expect(root.textContent).toContain('Información del colegio')
 expect(root.textContent).not.toContain('Mis documentos')
 expect(root.textContent).not.toContain('text/plain')
 expect(vi.mocked(get).mock.calls.some(([url])=>String(url).startsWith('/api/documents?'))).toBe(true)
 const upload=[...root.querySelectorAll('button')].find(b=>b.textContent?.includes('Subir archivo'))!
 expect(upload).toBeDefined();upload.click();await nextTick()
 await vi.waitFor(()=>expect(document.querySelector('#document-title')).not.toBeNull())
 expect(document.querySelector<HTMLInputElement>('#document-title')?.maxLength).toBe(100)
 expect(document.querySelector<HTMLInputElement>('#document-file')?.accept).toBe('text/plain')
 expect(document.querySelector('#file-help')?.textContent).toContain('1.0 KiB')
 expect(document.querySelector('#file-help')?.textContent).not.toContain('20 MiB')
 expect(getWebModule(router,'documents')?.home?.collection.to).toBe('/documents')
})
it('removes the installed collection routes when it becomes unavailable',async()=>{
 const router=createRouter({history:createMemoryHistory(),routes:[{path:'/',component:{template:'<p />'}}]})
 await registerModules(router,[module]);expect(router.hasRoute('documents')).toBe(true)
 await registerModules(router,[{...module,available:false}]);expect(router.hasRoute('documents')).toBe(false);expect(router.hasRoute('documents-detail')).toBe(false)
})
