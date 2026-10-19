export type {
  QrCreateResponse,
  QrPollResponse,
  QrInspectResponse,
  QrApproveResponse,
} from '../../api/client'

export interface ParsedQrPayload {
  id: string
  scan_secret: string
}

export type QrValidationResult =
  | { valid: true; id: string; scan_secret: string }
  | { valid: false; reason: string }
