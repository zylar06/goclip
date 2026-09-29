import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'
import { TextDecoder, TextEncoder } from 'node:util'
import '../src/i18n'

Object.assign(globalThis, { TextEncoder, TextDecoder })
Object.defineProperty(HTMLMediaElement.prototype, 'pause', { configurable: true, value: vi.fn() })
Object.defineProperty(HTMLMediaElement.prototype, 'play', { configurable: true, value: vi.fn().mockResolvedValue(undefined) })
afterEach(() => { cleanup(); localStorage.clear(); vi.useRealTimers(); vi.unstubAllGlobals() })
