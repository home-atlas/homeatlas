import { inject, type InjectionKey } from 'vue'
import { ApiError } from '@/core/api'
export interface CollectionDefinition {title:string;description:string;titleMaxLength:number;descriptionMaxLength:number;maxTags:number;tagMaxLength:number;categories:{value:string;label:string}[];mediaTypes:string[];maxFileBytes:number;legacySnapshot?:boolean}
export interface ItemMetadata {title:string;category:string;description:string;tags:string[]}
export interface Item extends ItemMetadata {ownerId:string;ownerName:string;id:string;createdAt:string;filename:string;mediaType:string;size:number;sha256:string;hasFile:boolean}
export const collectionContext: InjectionKey<{id:string;definition:CollectionDefinition}> = Symbol('collection')
export function useCollection(){
 const context=inject(collectionContext)
 if (!context) throw new Error('Falta la colección instalada.')
 const config=context.definition, categories=config.categories
 const mediaLabels:Record<string,string>={'application/pdf':'PDF','image/png':'PNG','image/jpeg':'JPEG','text/plain':'TXT'}
 return {collectionId:context.id,config,categories,fileTypes:config.mediaTypes.map(type=>mediaLabels[type]??'Archivo'),categoryLabel:(value:string)=>categories.find(c=>c.value===value)?.label??value,validateFile:(file:File)=>{
  if (!file.size) return 'El archivo está vacío.'
  if(file.size>config.maxFileBytes) return 'El archivo supera el límite permitido.'
  if(file.type && !config.mediaTypes.includes(file.type)) return 'El formato no es compatible con esta colección.'
  return ''
 }}
}
export {formatSize} from '@/core/format-size'
export {formatHouseholdDate as formatDate} from '@/core/datetime'
export function errorMessage(error:unknown):string{
 if (!(error instanceof ApiError)) return 'No pudimos conectar con el servidor. Revisa la conexión e inténtalo de nuevo.'
 const messages:Record<number,string>={401:'Tu sesión ha caducado. Vuelve a iniciar sesión.',403:'No tienes permiso para realizar esta acción.',404:'El archivo no está disponible o no tienes acceso.',413:'El archivo supera el límite permitido.',415:'El formato o el contenido del archivo no es compatible.',409:'El archivo original no está disponible. Consulta a quien administra el servidor.',429:'Hay otras operaciones en curso. Espera unos segundos.',400:'Revisa el archivo y sus metadatos.'}
 return messages[error.status]??'No pudimos completar la solicitud. Inténtalo de nuevo.'
}
