export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly retryAfter: number
  constructor(status: number, code = 'request_failed', retryAfter = 0) {
    super(`La solicitud no pudo completarse (${status}).`)
    this.status = status
    this.code = code
    this.retryAfter = retryAfter
  }
}

export async function get<T>(path: string): Promise<T> {
  return request<T>(path, 'GET')
}

export async function post<T = void>(path: string, body: unknown = {}, timeoutMs = 5000): Promise<T> {
  return request<T>(path, 'POST', body, timeoutMs)
}

export async function patch<T>(path: string, body: unknown, timeoutMs = 5000): Promise<T> {
  return request<T>(path, 'PATCH', body, timeoutMs)
}

export async function remove(path: string): Promise<void> { return request<void>(path, 'DELETE') }
export async function put(path: string): Promise<void> { return request<void>(path, 'PUT') }

export async function upload<T>(path: string, body: FormData): Promise<T> {
  return request<T>(path, 'POST', body)
}

export async function download(path: string): Promise<Blob> {
  const response = await fetch(path, { credentials: 'same-origin', signal: AbortSignal.timeout(120000) })
  await checkResponse(response)
  return response.blob()
}

export async function checkResponse(response: Response) {
  if (!response.ok) {
    const error = await response.json().catch(() => null) as { error?: string } | null
    throw new ApiError(response.status, error?.error, Number(response.headers.get('Retry-After')) || 0)
  }
}

async function request<T>(path: string, method: string, body?: unknown, timeoutMs = 5000): Promise<T> {
  const multipart = body instanceof FormData
  const response = await fetch(path, {
    method,
    headers: { Accept: 'application/json', ...(body === undefined || multipart ? {} : { 'Content-Type': 'application/json' }) },
    ...(body === undefined ? {} : { body: multipart ? body : JSON.stringify(body) }),
    credentials: 'same-origin',
    signal: AbortSignal.timeout(multipart ? 120000 : timeoutMs),
  })
  await checkResponse(response)
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}
