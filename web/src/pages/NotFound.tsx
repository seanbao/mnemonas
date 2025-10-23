import { Button } from '@heroui/react'
import { Home, ArrowLeft } from 'lucide-react'
import { useNavigate } from 'react-router-dom'

export function NotFoundPage() {
  const navigate = useNavigate()

  return (
    <div className="relative flex min-h-[60vh] flex-col items-center justify-center px-4 text-center">
      <div className="relative z-10">
        <h1 className="text-8xl font-bold text-primary/55 sm:text-9xl">404</h1>
        <h2 className="text-2xl font-semibold mt-4 text-foreground">页面不存在</h2>
        <p className="text-default-500 mt-2 max-w-md">
          该页面可能已被移动或删除，请检查 URL 是否正确。
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
