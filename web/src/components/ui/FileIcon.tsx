import type { ComponentType } from 'react'
import {
	Folder,
	File,
	Image,
	Video,
	Music,
	FileText,
	FileCode,
	Archive,
} from 'lucide-react'
import { cn, getFileIcon } from '@/lib/utils'

type FileIconVariant = 'tile' | 'bare'

interface FileIconProps {
	name: string
	isDir: boolean
	size?: number
	variant?: FileIconVariant
	className?: string
}

export function FileIcon({
	name,
	isDir,
	size = 20,
	variant = 'tile',
	className,
}: FileIconProps) {
	const iconType = getFileIcon(name, isDir)

	const icons: Record<string, ComponentType<{ size?: number; className?: string }>> = {
		folder: Folder,
		file: File,
		image: Image,
		video: Video,
		audio: Music,
		document: FileText,
		code: FileCode,
		archive: Archive,
	}

	const Icon = icons[iconType] || File

	const tileColors: Record<string, string> = {
		folder: 'bg-primary/10 text-primary',
		image: 'bg-success/10 text-success',
		video: 'bg-secondary/10 text-secondary',
		audio: 'bg-warning/10 text-warning',
		document: 'bg-primary/10 text-primary',
		code: 'bg-danger/10 text-danger',
		archive: 'bg-warning/10 text-warning',
		file: 'bg-content2 text-default-500',
	}

	const bareColors: Record<string, string> = {
		folder: 'text-primary',
		image: 'text-success',
		video: 'text-secondary',
		audio: 'text-warning',
		document: 'text-primary',
		code: 'text-danger',
		archive: 'text-warning',
		file: 'text-default-500',
	}

	if (variant === 'bare') {
		return <Icon size={size} className={cn(bareColors[iconType] || bareColors.file, className)} />
	}

	const sizeClass = size >= 28 ? 'w-10 h-10' : size >= 22 ? 'w-9 h-9' : 'w-7 h-7'
	const iconSize = Math.round(size * 0.6)

	return (
		<div
			className={cn(
				'flex items-center justify-center rounded-lg border border-divider glass',
				sizeClass,
				tileColors[iconType] || tileColors.file,
				className,
			)}
		>
			<Icon size={iconSize} className="text-current" />
		</div>
	)
}