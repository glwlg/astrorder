export type ConfirmationCoordinates = { x: number; y: number } | null

export function confirmationCoordinatesFromEvent(event?: { clientX?: number; clientY?: number } | null): ConfirmationCoordinates {
  const x = event?.clientX
  const y = event?.clientY
  if (typeof x !== 'number' || typeof y !== 'number' || (x === 0 && y === 0)) return null
  return { x, y }
}
