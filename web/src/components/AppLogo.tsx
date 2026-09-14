export default function AppLogo({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 40 40" fill="none" aria-hidden="true">
      <rect x="6.5" y="3.5" width="27" height="33" rx="5.5" stroke="currentColor" strokeWidth="3" />
      <path d="M20 10.25c.76 3.8 2.95 5.99 6.75 6.75-3.8.76-5.99 2.95-6.75 6.75-.76-3.8-2.95-5.99-6.75-6.75 3.8-.76 5.99-2.95 6.75-6.75Z" fill="currentColor" />
      <circle cx="27.5" cy="27.5" r="2" fill="currentColor" />
    </svg>
  )
}
