import { Button } from '@heroui/react'
import { Home, ArrowLeft } from 'lucide-react'
import { useNavigate } from 'react-router-dom'

export function NotFoundPage() {
  const navigate = useNavigate()

  return (
    <div className="flex flex-col items-center justify-center min-h-[60vh] text-center px-4">
      <h1 className="text-9xl font-bold text-primary/20">404</h1>
      <h2 className="text-2xl font-semibold mt-4">页面不存在</h2>
      <p className="text-default-500 mt-2 max-w-md">
        您访问的页面可能已被移动或删除，请检查 URL 是否正确。
      </p>
      <div className="flex items-center gap-4 mt-8">
        <Button
          variant="flat"
          startContent={<ArrowLeft size={16} />}
          onPress={() => navigate(-1)}
        >
          返回上页
        </Button>
        <Button
          color="primary"
          startContent={<Home size={16} />}
          onPress={() => navigate('/')}
        >
          回到首页
        </Button>
      </div>
    </div>
  )
}
