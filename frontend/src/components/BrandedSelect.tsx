import { useId, type ReactNode } from 'react'
import { Menu } from '@mantine/core'
import { IconCheck, IconChevronDown } from '@tabler/icons-react'
import './brandedSelect.css'

export interface BrandedSelectOption {
  value: string
  label: string
  description?: string
  icon?: ReactNode
  disabled?: boolean
}

export interface BrandedSelectProps {
  label?: string
  description?: string
  placeholder?: string
  value: string
  onChange: (value: string) => void
  options: readonly BrandedSelectOption[]
  disabled?: boolean
  zIndex?: number
  className?: string
}

export function BrandedSelect({
  label,
  description,
  placeholder = '请选择',
  value,
  onChange,
  options,
  disabled = false,
  zIndex = 520,
  className,
}: BrandedSelectProps) {
  const triggerId = useId()
  const labelId = useId()
  const active = options.find((opt) => opt.value === value)
  const displayLabel = active?.label || placeholder

  return (
    <div className={`astr-branded-select${className ? ` ${className}` : ''}`}>
      {label && (
        <label id={labelId} htmlFor={triggerId} className="astr-branded-select-label">
          {label}
        </label>
      )}
      {description && <div className="astr-branded-select-desc">{description}</div>}
      <Menu
        withinPortal
        hideDetached={false}
        position="bottom-start"
        offset={5}
        radius="md"
        shadow="lg"
        width="target"
        zIndex={zIndex}
        disabled={disabled}
      >
        <Menu.Target>
          <button
            id={triggerId}
            type="button"
            className={`astr-branded-select-trigger${!active?.value ? ' is-placeholder' : ''}`}
            aria-labelledby={label ? labelId : undefined}
            aria-label={label || placeholder}
            disabled={disabled}
          >
            <span className="astr-branded-select-value">
              {active?.icon && <span className="astr-branded-select-icon" aria-hidden="true">{active.icon}</span>}
              <span className="astr-branded-select-text">{displayLabel}</span>
            </span>
            <IconChevronDown size={14} className="astr-branded-select-chevron" aria-hidden="true" />
          </button>
        </Menu.Target>
        <Menu.Dropdown
          className="astr-branded-select-dropdown"
          data-liquid-surface="menu"
          data-liquid-engine="css"
        >
          <div className="astr-branded-select-scroll">
            {options.map((opt) => {
              const selected = opt.value === value
              return (
                <Menu.Item
                  key={opt.value || '__empty__'}
                  className={`astr-branded-select-item${selected ? ' is-selected' : ''}`}
                  disabled={opt.disabled}
                  leftSection={opt.icon ? <span className="astr-branded-select-icon">{opt.icon}</span> : undefined}
                  rightSection={selected ? <IconCheck size={14} color="var(--astr-indigo)" /> : null}
                  onClick={() => onChange(opt.value)}
                >
                  <div className="astr-branded-select-copy">
                    <span className="astr-branded-select-item-title">{opt.label}</span>
                    {opt.description && (
                      <span className="astr-branded-select-item-desc">{opt.description}</span>
                    )}
                  </div>
                </Menu.Item>
              )
            })}
          </div>
        </Menu.Dropdown>
      </Menu>
    </div>
  )
}
