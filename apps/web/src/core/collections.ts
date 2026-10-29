import { defineAsyncComponent, defineComponent, h, provide } from 'vue'
import type { ModuleStatus, WebModule } from './modules'
import { collectionContext, type Item } from '@/components/collections/model'
import { get } from './api'
import image from '@/assets/home-spaces.jpg'

// The package supplies declarative content. It cannot inject scripts, markup,
// CSS, routes outside its namespace, or acquire another module's credentials.
export function collectionFrontend(module:ModuleStatus):WebModule {
 const config=module.collection!
 if(!config.title||!Array.isArray(config.categories)||!config.categories.length) throw new Error('Colección incompatible.')
 const page=(detail:boolean)=>defineComponent({setup(){
  provide(collectionContext,{id:module.id,definition:config})
  const view=defineAsyncComponent(detail?()=>import('@/components/collections/CollectionDetailView.vue'):()=>import('@/components/collections/CollectionView.vue'))
  return ()=>h(view)
 }})
 return {id:module.id,routes:[{name:module.id,path:'/'+module.id,component:page(false)},{name:module.id+'-detail',path:'/'+module.id+'/:id',component:page(true)}],home:{
  caption:module.name,recentCaption:'Último archivo',recentTitle:'Añadidos recientemente',loadingText:'Cargando archivos…',icon:'document',image,tone:'amber',
  collection:{label:'Ver '+module.name,to:'/'+module.id},empty:{title:'Todo en su lugar',description:config.description,to:'/'+module.id},space:{description:config.description},
  async load(){const items=await get<Item[]>('/api/'+module.id);return items.slice(0,3).map(item=>({id:item.id,title:item.title,description:item.category,to:'/'+module.id+'/'+encodeURIComponent(item.id),date:item.createdAt}))}
 }}
}
