import { useEffect, useRef, useState } from 'react'
import type { InputHTMLAttributes } from 'react'

type Props = Omit<InputHTMLAttributes<HTMLInputElement>, 'onChange' | 'type' | 'value'> & {
  value: number | null
  onValueChange: (value: number | null) => void
  allowEmpty?: boolean
}

// Keeps the user's draft separate from the validated numeric value so an
// input can remain empty while its previous contents are being replaced.
export default function NumberInput({ value, onValueChange, allowEmpty = false, onBlur, onFocus, ...props }: Props) {
  const externalValue = value === null ? '' : String(value)
  const [draft, setDraft] = useState(externalValue)
  const focused = useRef(false)

  function normalized(input: number) {
    let next = props.step === 1 || props.step === '1' ? Math.trunc(input) : input
    const minimum = props.min === undefined ? Number.NEGATIVE_INFINITY : Number(props.min)
    const maximum = props.max === undefined ? Number.POSITIVE_INFINITY : Number(props.max)
    if (Number.isFinite(minimum)) next = Math.max(minimum, next)
    if (Number.isFinite(maximum)) next = Math.min(maximum, next)
    return next
  }

  useEffect(() => {
    if (!focused.current) setDraft(externalValue)
  }, [externalValue])

  return <input
    {...props}
    type="number"
    value={draft}
    onFocus={(event) => {
      focused.current = true
      onFocus?.(event)
    }}
    onChange={(event) => {
      const next = event.target.value
      setDraft(next)
      if (next === '') {
        if (allowEmpty) onValueChange(null)
        return
      }
      const parsed = event.target.valueAsNumber
      if (Number.isFinite(parsed)) onValueChange(normalized(parsed))
    }}
    onBlur={(event) => {
      focused.current = false
      if (draft === '' && !allowEmpty) setDraft(externalValue)
      else if (draft !== '' && Number.isFinite(event.target.valueAsNumber)) {
        const next = normalized(event.target.valueAsNumber)
        setDraft(String(next))
        onValueChange(next)
      }
      onBlur?.(event)
    }}
  />
}
