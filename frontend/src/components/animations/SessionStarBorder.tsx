import './SessionStarBorder.css'

// Shared React Bits Star Border adaptation; no wrapper around row interactions.
export function SessionStarBorder() {
  return <span className="session-star-border" data-status="running" aria-hidden="true">
    <span className="session-star-outline" />
    <span className="session-star-rim">
      <span className="session-star-glint session-star-top" />
      <span className="session-star-glint session-star-bottom" />
    </span>
  </span>
}
