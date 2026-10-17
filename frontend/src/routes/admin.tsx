import { SectionLayout } from '../components/SectionLayout'
import { t } from '../i18n'

export function AdminPage() {
  return (
    <SectionLayout
      title={t('admin.title')}
      intro={t('admin.intro')}
      label={t('admin.sectionsLabel')}
      links={[
        { to: '/settings', label: t('admin.settings') },
        { to: '/management', label: t('admin.management') },
        { to: '/maintenance', label: t('admin.maintenance') },
      ]}
    />
  )
}
