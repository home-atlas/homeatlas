<p align="center">
  <img src="apps/web/src/assets/homeatlas-mark.svg" alt="Icono de HomeAtlas" width="96" />
</p>

<h1 align="center">HomeAtlas</h1>

<p align="center">
  Un lugar para los documentos y la organización de tu hogar.<br />
  <strong>Autohospedado · Modular · Local por defecto</strong>
</p>

HomeAtlas es una plataforma familiar que instalas en tu propio servidor para guardar documentos, encontrar información y compartirla con las personas de tu hogar. Funciona con un proceso Go, una interfaz web y SQLite, sin servicios externos obligatorios.

El núcleo gestiona cuentas, hogares y permisos. Las funciones de producto se incorporan mediante plugins independientes: **Documentos** es el primero y se instala sin recompilar el núcleo.

> **Estado:** desarrollo activo. El contrato de plugins es experimental. La licencia del proyecto está pendiente de formalizar; consulta [Licencia](#licencia).

## Qué puedes hacer

| Función | Disponible |
| --- | --- |
| Documentos | Subir PDF, PNG, JPEG y TXT de hasta 20 MiB; editar metadatos, filtrar y buscar por título, descripción o etiquetas. |
| Familia y permisos | Invitar miembros, asignar roles y compartir lectura y descarga de documentos con personas concretas. |
| Almacenamiento | Usar disco local o un bucket S3 compatible; trasladar documentos existentes entre ambos destinos desde Configuración. |
| Copias de seguridad | Crear copias cifradas, programarlas y restaurar datos o recuperar un hogar en una instalación nueva. |
| Acceso | Iniciar sesión con usuario o correo y contraseña; preparar acceso asistido con PIN vinculado al dispositivo para cuentas elegibles. |
| Plugins | Instalar paquetes WASI aprobados mediante CLI y reinicio, sin reconstruir el núcleo. |

La búsqueda actual utiliza metadatos: no incluye OCR ni búsqueda dentro del contenido de los archivos. Las funciones de IA, inventario y garantías forman parte del roadmap.

## Inicio rápido

### Requisitos

- **Go 1.27.1 o superior**.
- **Node.js 24 o superior**, con npm.
- **Make**.
- **Python 3** para empaquetar plugins y ejecutar sus pruebas de integración.

### Compilar y arrancar

```sh
git clone https://github.com/home-atlas/homeatlas.git
cd homeatlas
make install
make build
./bin/homeatlas
```

Abre [http://127.0.0.1:8080](http://127.0.0.1:8080).

En el primer arranque se genera un código privado en `data/setup-code`. Consúltalo desde otra terminal del servidor:

```sh
cat ./data/setup-code
```

Úsalo en la pantalla de alta para crear la cuenta de propietario y el hogar. No hay credenciales predeterminadas ni registro público abierto; después del alta, los demás miembros acceden mediante invitación.

El núcleo arranca con un catálogo vacío. Para añadir la biblioteca de documentos, instala el plugin siguiente.

## Instalar Documentos

Documentos se compila como un paquete `.hap` separado del ejecutable del servidor.

```sh
make plugin-documents PACKAGE_OUT=bin/plugins/documents.hap
```

El empaquetador muestra el SHA-256 del paquete. **Detén el servidor** y sustituye `SHA256_DEL_PAQUETE` por ese valor antes de instalarlo:

```sh
./bin/homeatlas plugins install \
  -data ./data \
  -file ./bin/plugins/documents.hap \
  -sha256 SHA256_DEL_PAQUETE \
  -allow documents.read,documents.write,documents.share,documents.files,documents.collection

./bin/homeatlas
```

El paquete aparecerá en **Mis módulos**. Si ya existe el archivo de salida, usa otra ruta en `PACKAGE_OUT`: el empaquetador no sobrescribe paquetes.

Para consultar los paquetes instalados:

```sh
./bin/homeatlas plugins list -data ./data
```

Instalar un paquete compatible requiere reiniciar el servidor, pero no recompilar el núcleo. La activación por hogar se administra desde la interfaz y conserva los datos y permisos al desactivarse. Otros miembros reciben únicamente los permisos que se les concedan.

## Local o S3: tú eliges

En **Configuración → Almacenamiento de archivos**, el propietario puede elegir el destino de las nuevas subidas:

- **Local**, seleccionado por defecto, guarda los originales en el servidor.
- **S3** utiliza un bucket privado, región y credenciales. Admite endpoint HTTPS y direccionamiento path-style para proveedores compatibles.

Antes de activar S3, HomeAtlas comprueba escritura, lectura y borrado. Las credenciales se guardan cifradas en el servidor y no se entregan al plugin ni se devuelven al navegador.

Cambiar el destino no mueve automáticamente los archivos anteriores. Para trasladarlos:

1. Guarda Local o S3 como destino.
2. Abre **Mover documentos existentes** y revisa la cantidad y el tamaño pendientes.
3. Confirma el traslado y mantén abierta la pestaña para que continúe el lote.

Cada copia se verifica por tamaño y SHA-256 antes de cambiar su ubicación y retirar el original. Se conservan identificadores, metadatos y permisos. Puedes detener el proceso al terminar el archivo en curso y continuar después; una limpieza fallida queda pendiente para reintento.

S3 se aplica a los originales de las colecciones. SQLite, los paquetes instalados y las copias automáticas permanecen en el servidor local.

## Privacidad y recuperación

Los documentos son privados para su propietario hasta que los comparta expresamente. Los destinatarios autorizados pueden consultarlos y descargarlos; no editarlos ni volver a compartirlos. Ser administrador no concede acceso de navegación a todos los documentos privados.

Las copias completas del propietario **sí incluyen los datos privados de los módulos del hogar**. Se cifran con contraseña y pueden descargarse o generarse automáticamente en el servidor. Guarda la contraseña por separado y conserva copias fuera del equipo que ejecuta HomeAtlas.

Para recuperar módulos externos, conserva también el paquete `.hap` exacto: las copias contienen datos, no instalan código ejecutable. Las credenciales S3, la configuración SMTP, las sesiones y los vínculos de dispositivos PIN no se exportan. En una instalación nueva se configuran de nuevo.

Las copias incluyen los originales locales y S3. Al restaurarlos se escriben en local, por lo que la recuperación no necesita las credenciales del proveedor de origen. El cifrado de las copias no cifra automáticamente la base de datos ni los originales locales en uso.

## Arquitectura

| Capa | Tecnología |
| --- | --- |
| Servidor | Go |
| Interfaz | Vue 3, TypeScript, Vite y shadcn-vue |
| Metadatos y búsqueda | SQLite con WAL y FTS5 |
| Archivos | Disco local o S3 compatible |
| Plugins externos | WASI Preview 1, ejecutado con wazero |
| Copias cifradas | age |

La web compilada se integra en el ejecutable de Go. Los plugins aportan su manifiesto y código WASI; el núcleo proporciona componentes de interfaz y operaciones de almacenamiento autorizadas. Los invitados no reciben acceso directo a SQL, archivos, red ni credenciales, y no distribuyen JavaScript de terceros al navegador.

```text
apps/web/             Interfaz y componentes compartidos
cmd/homeatlas/        Servidor y comandos CLI
internal/core/        Identidad, permisos, módulos, almacenamiento y copias
internal/app/         Integración HTTP y pruebas de aplicación
plugins/documents/    Fuente y manifiesto del plugin Documentos
sdk/pluginstorage/    SDK experimental de almacenamiento para WASI
scripts/              Empaquetado, verificación y medición
```

## Desarrollo

Instala las dependencias con `make install` y ejecuta en dos terminales:

```sh
# Terminal 1: API
make dev-api
```

```sh
# Terminal 2: interfaz con recarga automática
make dev-web
```

Abre [http://127.0.0.1:5173](http://127.0.0.1:5173). Si necesitas otro puerto para la interfaz, usa el mismo `WEB_PORT` en ambos comandos.

### Pruebas

```sh
make test
```

Incluye pruebas Go con detección de carreras, migración desde versiones anteriores, integración con plugins WASI reales, análisis estático, pruebas de interfaz y compilación web. La [configuración de CI](.github/workflows/ci.yml) ejecuta estos checks y construye el servidor.

También puedes verificar la instalación independiente de Documentos con datos temporales:

```sh
python3 scripts/verify-documents-plugin.py \
  --binary ./bin/homeatlas \
  --package ./bin/plugins/documents.hap
```

Este recorrido comprueba instalación sin modificar el ejecutable, carga, búsqueda, descarga, copia cifrada y reinicio.

## Despliegue

La instalación actual se construye desde el código fuente. Ejecuta **un solo proceso por directorio de datos** y conserva ese directorio en almacenamiento persistente.

Para publicar la aplicación detrás de un proxy inverso con HTTPS:

```sh
./bin/homeatlas \
  -addr 127.0.0.1:8080 \
  -public-url https://homeatlas.example \
  -data ./data
```

Configura el proxy para conservar el encabezado `Host`. HTTPS es obligatorio fuera de loopback; `-public-url` debe coincidir con el origen que utiliza el navegador.

SMTP es opcional y está desactivado por defecto. Consulta [.env.example](.env.example): HomeAtlas lee variables del proceso, no carga archivos `.env` automáticamente.

## Roadmap

- Distribución y despliegue reproducibles en más entornos.
- Evolución y estabilización del contrato público de plugins.
- Actualización y desinstalación de paquetes desde flujos administrados.
- Módulos de inventario y garantías.
- OCR e IA opcional sobre información autorizada.

El runtime y los plugins siguen siendo experimentales. Las pruebas de S3 utilizan un servidor TLS de ensayo; verifica la conexión con tu proveedor antes de usarlo. Las mediciones históricas de memoria no certifican todas las cargas ni una instalación mínima universal.

## Contribuir

Son bienvenidos los reportes de errores, las mejoras de documentación y las contribuciones de código. Incluye pasos para reproducir el problema y describe el comportamiento esperado; para cambios amplios, explica primero el alcance de la propuesta.

Antes de enviar un cambio, ejecuta `make test`. Conserva la compatibilidad de datos y permisos, añade pruebas para comportamientos nuevos y utiliza los componentes compartidos de la interfaz. No incluyas credenciales, datos de hogares, cachés ni artefactos de compilación.

## Licencia

La licencia de HomeAtlas está pendiente de definir. Este repositorio todavía no incluye un archivo `LICENSE` del proyecto.

Las dependencias y los componentes de terceros conservan sus propias licencias; la de los componentes shadcn-vue se encuentra [junto a su código](apps/web/src/components/ui/LICENSE.shadcn-vue).
