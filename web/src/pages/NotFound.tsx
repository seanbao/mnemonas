import { Button } from '@heroui/react'
import { Home, ArrowLeft } from 'lucide-react'
import { useNavigate } from 'react-router-dom'

export function NotFoundPage() {
  const navigate = useNavigate()

  return (
    <div className="flex flex-col items-center justify-center min-h-[60vh] text-center px-4 relative">
      {/* Background decoration */}
      <div className="absolute inset-0 overflow-hidden pointer-events-none">
        <div className="ambient-orb w-96 h-96 -top-48 -left-48 opacity-30" />
        <div className="ambient-orb w-64 h-64 -bottom-32 -right-32 opacity-20" />
      </div>
      
      <div className="relative z-10">
        <h1 className="text-9xl font-bold bg-gradient-to-br from-accent-primary/40 to-violet-500/40 bg-clip-text text-transparent">404</h1>
        <h2 className="text-2xl font-semibold mt-4 text-foreground">页面不存在</h2>
        <p className="text-default-500 mt-2 max-w-md">
          您访问的页面可能已被移动或删除，请检查 URL 是否正确。
        </p>
        <div className="flex items-center justify-center gap-4 mt-8">
          <Button
            variant="bordered"
            className="btn-secondary"
            startContent={<ArrowLeft size={16} />}
            onPress={() => navigate(-1)}
          >
            返回上页
          </Button>
          <Button
            className="btn-primary"
            startContent={<Home size={16} />}
            onPress={() => navigate('/')}
          >
            回到首页
          </Button>
        </div>
      </div>
    </div>
  )
}
