import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { HeroUIProvider, ToastProvider } from '@heroui/react'
import { QueryClientProvider } from '@tanstack/react-query'
import './index.css'
import App from './App.tsx'
import { queryClient } from '@/lib/queryClient'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <HeroUIProvider>
        <ToastProvider
          placement="top-right"
          toastOffset={76}
          regionProps={{ className: 'mn-toast-region' }}
          toastProps={{
            classNames: {
              motionDiv: 'mn-toast-motion',
              base: 'mn-toast',
              content: 'mn-toast-content',
              title: 'mn-toast-title',
              description: 'mn-toast-description',
            },
          }}
        />
        <App />
      </HeroUIProvider>
    </QueryClientProvider>
  </StrictMode>,
)
