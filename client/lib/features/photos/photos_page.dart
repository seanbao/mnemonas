import 'package:flutter/material.dart';

import '../../design_system/design_system.dart';

enum PhotosFeatureStatus { unavailable, loading, ready, error }

final class PhotosViewModel {
  const PhotosViewModel._({
    required this.status,
    this.photoCount,
    this.message,
  });

  const PhotosViewModel.unavailable({
    String message = '当前客户端尚未接入相册索引。图片仍可从文件页查找和打开。',
  }) : this._(status: PhotosFeatureStatus.unavailable, message: message);

  const PhotosViewModel.loading() : this._(status: PhotosFeatureStatus.loading);

  const PhotosViewModel.ready({required int photoCount})
    : this._(status: PhotosFeatureStatus.ready, photoCount: photoCount);

  const PhotosViewModel.error(String message)
    : this._(status: PhotosFeatureStatus.error, message: message);

  final PhotosFeatureStatus status;
  final int? photoCount;
  final String? message;
}

class PhotosPage extends StatelessWidget {
  const PhotosPage({
    super.key,
    required this.viewModel,
    required this.onBrowseImageFiles,
    this.onRefresh,
  });

  final PhotosViewModel viewModel;
  final VoidCallback onBrowseImageFiles;
  final Future<void> Function()? onRefresh;

  @override
  Widget build(BuildContext context) {
    return switch (viewModel.status) {
      PhotosFeatureStatus.loading => const MnemoContentFrame(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: <Widget>[
            MnemoSkeleton(width: 140, height: 28),
            SizedBox(height: MnemoSpacing.xl),
            MnemoSkeleton(height: 220),
          ],
        ),
      ),
      PhotosFeatureStatus.error => MnemoContentFrame(
        alignment: Alignment.center,
        child: MnemoErrorNotice(
          title: '相册信息加载失败',
          message: viewModel.message ?? '设备没有返回相册信息',
          onRetry: onRefresh,
        ),
      ),
      PhotosFeatureStatus.unavailable => MnemoContentFrame(
        alignment: Alignment.center,
        child: MnemoEmptyState(
          icon: Icons.photo_library_outlined,
          title: '相册功能尚未接入',
          message: viewModel.message ?? '当前客户端尚未接入相册索引。图片仍可从文件页查找和打开。',
          primaryAction: FilledButton.icon(
            onPressed: onBrowseImageFiles,
            icon: const Icon(Icons.folder_open_rounded),
            label: const Text('前往文件'),
          ),
        ),
      ),
      PhotosFeatureStatus.ready => _PhotosReady(
        photoCount: viewModel.photoCount ?? 0,
        onBrowseImageFiles: onBrowseImageFiles,
        onRefresh: onRefresh,
      ),
    };
  }
}

class _PhotosReady extends StatelessWidget {
  const _PhotosReady({
    required this.photoCount,
    required this.onBrowseImageFiles,
    required this.onRefresh,
  });

  final int photoCount;
  final VoidCallback onBrowseImageFiles;
  final Future<void> Function()? onRefresh;

  @override
  Widget build(BuildContext context) {
    final Widget content = ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: MnemoAdaptive.pagePaddingFor(
        MnemoAdaptive.windowClassFor(MediaQuery.sizeOf(context).width),
      ),
      children: <Widget>[
        const MnemoSectionTitle(title: '相册', description: '设备已返回的图片索引摘要'),
        const SizedBox(height: MnemoSpacing.xl),
        MnemoCard(
          tone: MnemoCardTone.elevated,
          child: Row(
            children: <Widget>[
              DecoratedBox(
                decoration: BoxDecoration(
                  color: Theme.of(context).colorScheme.primaryContainer,
                  borderRadius: BorderRadius.circular(MnemoRadius.md),
                ),
                child: Padding(
                  padding: const EdgeInsets.all(MnemoSpacing.lg),
                  child: Icon(
                    Icons.photo_library_rounded,
                    size: 34,
                    color: Theme.of(context).colorScheme.onPrimaryContainer,
                  ),
                ),
              ),
              const SizedBox(width: MnemoSpacing.md),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: <Widget>[
                    Text(
                      '$photoCount 张图片',
                      style: Theme.of(context).textTheme.titleLarge,
                    ),
                    const SizedBox(height: MnemoSpacing.xxs),
                    Text(
                      '初版客户端暂不提供时间线和智能分类。',
                      style: Theme.of(context).textTheme.bodySmall?.copyWith(
                        color: Theme.of(context).colorScheme.onSurfaceVariant,
                      ),
                    ),
                  ],
                ),
              ),
              IconButton(
                onPressed: onBrowseImageFiles,
                tooltip: '在文件中查看',
                icon: const Icon(Icons.arrow_forward_rounded),
              ),
            ],
          ),
        ),
      ],
    );
    if (onRefresh == null) {
      return content;
    }
    return RefreshIndicator(onRefresh: onRefresh!, child: content);
  }
}
