import 'package:flutter/material.dart';

import '../../design_system/design_system.dart';

enum NasConnectionStatus { online, connecting, offline }

enum HomeActivityType { upload, download, createFolder, delete, other }

enum HomeAlertSeverity { info, warning, critical }

enum HomeLoadState { loading, ready, error }

final class StorageSummary {
  const StorageSummary({required this.usedBytes, required this.totalBytes});

  final int usedBytes;
  final int totalBytes;

  double get usedRatio {
    if (totalBytes <= 0) {
      return 0;
    }
    return (usedBytes / totalBytes).clamp(0, 1);
  }
}

final class HomeActivity {
  const HomeActivity({
    required this.title,
    required this.detail,
    required this.occurredAt,
    this.type = HomeActivityType.other,
  });

  final String title;
  final String detail;
  final DateTime occurredAt;
  final HomeActivityType type;
}

final class HomeAlert {
  const HomeAlert({
    required this.id,
    required this.title,
    required this.message,
    this.severity = HomeAlertSeverity.info,
    this.actionLabel,
  });

  final String id;
  final String title;
  final String message;
  final HomeAlertSeverity severity;
  final String? actionLabel;
}

final class HomeSummary {
  const HomeSummary({
    required this.deviceName,
    required this.serverAddress,
    required this.connectionStatus,
    required this.storage,
    this.activities = const <HomeActivity>[],
    this.alerts = const <HomeAlert>[],
    this.updatedAt,
  });

  final String deviceName;
  final String serverAddress;
  final NasConnectionStatus connectionStatus;
  final StorageSummary? storage;
  final List<HomeActivity> activities;
  final List<HomeAlert> alerts;
  final DateTime? updatedAt;
}

final class HomeViewModel {
  const HomeViewModel._({required this.state, this.summary, this.errorMessage});

  const HomeViewModel.loading() : this._(state: HomeLoadState.loading);

  const HomeViewModel.ready(HomeSummary summary)
    : this._(state: HomeLoadState.ready, summary: summary);

  const HomeViewModel.error(String message)
    : this._(state: HomeLoadState.error, errorMessage: message);

  final HomeLoadState state;
  final HomeSummary? summary;
  final String? errorMessage;
}

class HomePage extends StatelessWidget {
  const HomePage({
    super.key,
    required this.viewModel,
    required this.onRefresh,
    required this.onOpenFiles,
    this.onOpenAlert,
  });

  final HomeViewModel viewModel;
  final Future<void> Function() onRefresh;
  final VoidCallback onOpenFiles;
  final ValueChanged<HomeAlert>? onOpenAlert;

  @override
  Widget build(BuildContext context) {
    return switch (viewModel.state) {
      HomeLoadState.loading => const _HomeLoading(),
      HomeLoadState.error => _HomeError(
        message: viewModel.errorMessage ?? '首页信息加载失败',
        onRetry: onRefresh,
      ),
      HomeLoadState.ready => _HomeReady(
        summary: viewModel.summary!,
        onRefresh: onRefresh,
        onOpenFiles: onOpenFiles,
        onOpenAlert: onOpenAlert,
      ),
    };
  }
}

class _HomeReady extends StatelessWidget {
  const _HomeReady({
    required this.summary,
    required this.onRefresh,
    required this.onOpenFiles,
    required this.onOpenAlert,
  });

  final HomeSummary summary;
  final Future<void> Function() onRefresh;
  final VoidCallback onOpenFiles;
  final ValueChanged<HomeAlert>? onOpenAlert;

  @override
  Widget build(BuildContext context) {
    return RefreshIndicator(
      onRefresh: onRefresh,
      child: MnemoAdaptiveBuilder(
        builder: (BuildContext context, MnemoWindowClass windowClass) {
          final EdgeInsets padding = MnemoAdaptive.pagePaddingFor(windowClass);
          final int columns = windowClass == MnemoWindowClass.compact ? 1 : 2;
          return ListView(
            key: const Key('home-ready'),
            physics: const AlwaysScrollableScrollPhysics(),
            padding: padding,
            children: <Widget>[
              _WelcomeBlock(summary: summary),
              const SizedBox(height: MnemoSpacing.xl),
              GridView.count(
                shrinkWrap: true,
                physics: const NeverScrollableScrollPhysics(),
                crossAxisCount: columns,
                mainAxisSpacing: MnemoSpacing.md,
                crossAxisSpacing: MnemoSpacing.md,
                childAspectRatio: columns == 1 ? 2.05 : 1.72,
                children: <Widget>[
                  _ConnectionCard(summary: summary),
                  _StorageCard(
                    storage: summary.storage,
                    onOpenFiles: onOpenFiles,
                  ),
                ],
              ),
              const SizedBox(height: MnemoSpacing.xl),
              _AlertSection(alerts: summary.alerts, onOpenAlert: onOpenAlert),
              const SizedBox(height: MnemoSpacing.xl),
              _ActivitySection(activities: summary.activities),
              const SizedBox(height: MnemoSpacing.xxl),
            ],
          );
        },
      ),
    );
  }
}

class _WelcomeBlock extends StatelessWidget {
  const _WelcomeBlock({required this.summary});

  final HomeSummary summary;

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: <Widget>[
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: <Widget>[
              Text(
                summary.deviceName,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: Theme.of(context).textTheme.headlineSmall,
              ),
              const SizedBox(height: MnemoSpacing.xxs),
              Text(
                summary.serverAddress,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
              ),
            ],
          ),
        ),
        const SizedBox(width: MnemoSpacing.md),
        _ConnectionPill(status: summary.connectionStatus),
      ],
    );
  }
}

class _ConnectionCard extends StatelessWidget {
  const _ConnectionCard({required this.summary});

  final HomeSummary summary;

  @override
  Widget build(BuildContext context) {
    final ({String title, String message, IconData icon, MnemoStatusTone tone})
    visual = switch (summary.connectionStatus) {
      NasConnectionStatus.online => (
        title: '设备连接正常',
        message: summary.updatedAt == null
            ? '可以访问文件和设备服务'
            : '最近更新 ${_relativeTime(summary.updatedAt!)}',
        icon: Icons.wifi_rounded,
        tone: MnemoStatusTone.success,
      ),
      NasConnectionStatus.connecting => (
        title: '正在重新连接',
        message: '等待设备响应',
        icon: Icons.sync_rounded,
        tone: MnemoStatusTone.warning,
      ),
      NasConnectionStatus.offline => (
        title: '设备暂时离线',
        message: '请检查设备电源和网络连接',
        icon: Icons.wifi_off_rounded,
        tone: MnemoStatusTone.danger,
      ),
    };
    return MnemoCard(
      tone: MnemoCardTone.elevated,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: <Widget>[
          Icon(
            visual.icon,
            size: 30,
            color: Theme.of(context).colorScheme.primary,
          ),
          const Spacer(),
          Text(visual.title, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: MnemoSpacing.xxs),
          Text(
            visual.message,
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
            style: Theme.of(context).textTheme.bodySmall?.copyWith(
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
          ),
        ],
      ),
    );
  }
}

class _StorageCard extends StatelessWidget {
  const _StorageCard({required this.storage, required this.onOpenFiles});

  final StorageSummary? storage;
  final VoidCallback onOpenFiles;

  @override
  Widget build(BuildContext context) {
    final ColorScheme colors = Theme.of(context).colorScheme;
    return MnemoCard(
      tone: MnemoCardTone.elevated,
      onTap: onOpenFiles,
      semanticLabel: '打开文件',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: <Widget>[
          Row(
            children: <Widget>[
              Icon(Icons.pie_chart_outline_rounded, color: colors.primary),
              const Spacer(),
              const Icon(Icons.arrow_forward_rounded, size: 20),
            ],
          ),
          const Spacer(),
          Text('存储空间', style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: MnemoSpacing.xs),
          if (storage == null)
            Text(
              '设备未返回容量信息',
              style: Theme.of(
                context,
              ).textTheme.bodySmall?.copyWith(color: colors.onSurfaceVariant),
            )
          else ...<Widget>[
            ClipRRect(
              borderRadius: BorderRadius.circular(MnemoRadius.pill),
              child: LinearProgressIndicator(
                minHeight: 8,
                value: storage!.usedRatio,
                backgroundColor: colors.surfaceContainerHighest,
              ),
            ),
            const SizedBox(height: MnemoSpacing.xs),
            Text(
              '已用 ${_formatBytes(storage!.usedBytes)} / ${_formatBytes(storage!.totalBytes)}',
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: Theme.of(
                context,
              ).textTheme.bodySmall?.copyWith(color: colors.onSurfaceVariant),
            ),
          ],
        ],
      ),
    );
  }
}

class _AlertSection extends StatelessWidget {
  const _AlertSection({required this.alerts, required this.onOpenAlert});

  final List<HomeAlert> alerts;
  final ValueChanged<HomeAlert>? onOpenAlert;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: <Widget>[
        const MnemoSectionTitle(title: '主要提醒', description: '需要留意的设备和存储状态'),
        const SizedBox(height: MnemoSpacing.sm),
        if (alerts.isEmpty)
          const MnemoCard(
            tone: MnemoCardTone.muted,
            child: Row(
              children: <Widget>[
                Icon(Icons.check_circle_outline_rounded),
                SizedBox(width: MnemoSpacing.sm),
                Expanded(child: Text('当前没有需要处理的提醒')),
              ],
            ),
          )
        else
          ...alerts.map(
            (HomeAlert alert) => Padding(
              padding: const EdgeInsets.only(bottom: MnemoSpacing.sm),
              child: _AlertCard(
                alert: alert,
                onTap: alert.actionLabel == null || onOpenAlert == null
                    ? null
                    : () => onOpenAlert!(alert),
              ),
            ),
          ),
      ],
    );
  }
}

class _AlertCard extends StatelessWidget {
  const _AlertCard({required this.alert, required this.onTap});

  final HomeAlert alert;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final ColorScheme colors = Theme.of(context).colorScheme;
    final MnemoSemanticColors semantic = context.mnemoColors;
    final (Color, IconData) visual = switch (alert.severity) {
      HomeAlertSeverity.info => (semantic.info, Icons.info_outline_rounded),
      HomeAlertSeverity.warning => (
        semantic.warning,
        Icons.warning_amber_rounded,
      ),
      HomeAlertSeverity.critical => (colors.error, Icons.error_outline_rounded),
    };
    return MnemoCard(
      onTap: onTap,
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: <Widget>[
          Icon(visual.$2, color: visual.$1),
          const SizedBox(width: MnemoSpacing.sm),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: <Widget>[
                Text(
                  alert.title,
                  style: Theme.of(context).textTheme.titleSmall,
                ),
                const SizedBox(height: MnemoSpacing.xxs),
                Text(
                  alert.message,
                  style: Theme.of(context).textTheme.bodySmall?.copyWith(
                    color: colors.onSurfaceVariant,
                  ),
                ),
              ],
            ),
          ),
          if (alert.actionLabel != null) ...<Widget>[
            const SizedBox(width: MnemoSpacing.xs),
            Text(
              alert.actionLabel!,
              style: Theme.of(
                context,
              ).textTheme.labelMedium?.copyWith(color: colors.primary),
            ),
          ],
        ],
      ),
    );
  }
}

class _ActivitySection extends StatelessWidget {
  const _ActivitySection({required this.activities});

  final List<HomeActivity> activities;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: <Widget>[
        const MnemoSectionTitle(title: '近期操作', description: '当前账户最近完成的文件操作'),
        const SizedBox(height: MnemoSpacing.sm),
        MnemoCard(
          padding: activities.isEmpty
              ? const EdgeInsets.all(MnemoSpacing.lg)
              : const EdgeInsets.symmetric(vertical: MnemoSpacing.xs),
          child: activities.isEmpty
              ? Text(
                  '暂无近期操作',
                  textAlign: TextAlign.center,
                  style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                )
              : Column(
                  children: <Widget>[
                    for (int index = 0; index < activities.length; index++) ...[
                      _ActivityTile(activity: activities[index]),
                      if (index != activities.length - 1)
                        const Divider(indent: 64),
                    ],
                  ],
                ),
        ),
      ],
    );
  }
}

class _ActivityTile extends StatelessWidget {
  const _ActivityTile({required this.activity});

  final HomeActivity activity;

  @override
  Widget build(BuildContext context) {
    final IconData icon = switch (activity.type) {
      HomeActivityType.upload => Icons.upload_rounded,
      HomeActivityType.download => Icons.download_rounded,
      HomeActivityType.createFolder => Icons.create_new_folder_outlined,
      HomeActivityType.delete => Icons.delete_outline_rounded,
      HomeActivityType.other => Icons.history_rounded,
    };
    return ListTile(
      leading: CircleAvatar(
        backgroundColor: Theme.of(context).colorScheme.primaryContainer,
        foregroundColor: Theme.of(context).colorScheme.onPrimaryContainer,
        child: Icon(icon, size: 21),
      ),
      title: Text(activity.title),
      subtitle: Text(activity.detail),
      trailing: Text(
        _relativeTime(activity.occurredAt),
        style: Theme.of(context).textTheme.bodySmall?.copyWith(
          color: Theme.of(context).colorScheme.onSurfaceVariant,
        ),
      ),
    );
  }
}

class _ConnectionPill extends StatelessWidget {
  const _ConnectionPill({required this.status});

  final NasConnectionStatus status;

  @override
  Widget build(BuildContext context) {
    return switch (status) {
      NasConnectionStatus.online => const MnemoStatusPill(
        label: '已连接',
        tone: MnemoStatusTone.success,
      ),
      NasConnectionStatus.connecting => const MnemoStatusPill(
        label: '连接中',
        tone: MnemoStatusTone.warning,
        liveRegion: true,
      ),
      NasConnectionStatus.offline => const MnemoStatusPill(
        label: '离线',
        tone: MnemoStatusTone.danger,
        liveRegion: true,
      ),
    };
  }
}

class _HomeLoading extends StatelessWidget {
  const _HomeLoading();

  @override
  Widget build(BuildContext context) {
    return const MnemoContentFrame(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: <Widget>[
          MnemoSkeleton(width: 180, height: 28),
          SizedBox(height: MnemoSpacing.xs),
          MnemoSkeleton(width: 260, height: 14),
          SizedBox(height: MnemoSpacing.xl),
          MnemoSkeleton(height: 160),
          SizedBox(height: MnemoSpacing.md),
          MnemoSkeleton(height: 160),
        ],
      ),
    );
  }
}

class _HomeError extends StatelessWidget {
  const _HomeError({required this.message, required this.onRetry});

  final String message;
  final Future<void> Function() onRetry;

  @override
  Widget build(BuildContext context) {
    return MnemoContentFrame(
      alignment: Alignment.center,
      child: MnemoErrorNotice(
        title: '首页信息加载失败',
        message: message,
        onRetry: onRetry,
      ),
    );
  }
}

String _formatBytes(int bytes) {
  if (bytes < 1024) {
    return '$bytes B';
  }
  const units = <String>['KB', 'MB', 'GB', 'TB', 'PB'];
  double value = bytes / 1024;
  var unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  final digits = value >= 100
      ? 0
      : value >= 10
      ? 1
      : 2;
  return '${value.toStringAsFixed(digits)} ${units[unit]}';
}

String _relativeTime(DateTime value) {
  final Duration difference = DateTime.now().difference(value.toLocal());
  if (difference.isNegative || difference.inMinutes < 1) {
    return '刚刚';
  }
  if (difference.inHours < 1) {
    return '${difference.inMinutes} 分钟前';
  }
  if (difference.inDays < 1) {
    return '${difference.inHours} 小时前';
  }
  if (difference.inDays < 7) {
    return '${difference.inDays} 天前';
  }
  final DateTime local = value.toLocal();
  return '${local.month}月${local.day}日';
}
