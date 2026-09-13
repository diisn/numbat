// 左侧导航栏：页面切换
export type PageView = 'chat' | 'workbench' | 'runs' | 'settings'

interface NavItem {
  id: PageView
  label: string
  icon: string
}

const NAV_ITEMS: NavItem[] = [
  { id: 'chat', label: '对话', icon: '>' },
  { id: 'workbench', label: '工作台', icon: '#' },
  { id: 'runs', label: '运行记录', icon: '*' },
  { id: 'settings', label: '设置', icon: '=' },
]

interface NavRailProps {
  active: PageView
  onChange: (page: PageView) => void
}

export function NavRail({ active, onChange }: NavRailProps) {
  return (
    <nav className="nav-rail">
      <div className="nav-rail-logo">N</div>
      {NAV_ITEMS.map((item) => (
        <button
          key={item.id}
          type="button"
          className={`nav-rail-item ${active === item.id ? 'active' : ''}`}
          onClick={() => onChange(item.id)}
          title={item.label}
        >
          <span className="nav-rail-icon">{item.icon}</span>
          <span className="nav-rail-label">{item.label}</span>
        </button>
      ))}
    </nav>
  )
}
