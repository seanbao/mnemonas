import 'package:flutter/material.dart';

import '../../design_system/design_system.dart';
import '../shared/presentation_error.dart';

final class AccountViewModel {
  const AccountViewModel({
    required this.username,
    required this.roleLabel,
    required this.deviceName,
    required this.serverAddress,
    this.email,
    this.serverVersion,
    this.clientVersion,
    this.developmentNotice = '当前仍在开发阶段，尚未发布可用版本。',
  });

  final String username;
  final String roleLabel;
  final String deviceName;
  final String serverAddress;
  final String? email;
  final String? serverVersion;
  final String? clientVersion;
  final String? developmentNotice;
}

class AccountPage extends StatefulWidget {
  const AccountPage({
    super.key,
    required this.viewModel,
    required this.onChangeServer,
    required this.onLogout,
    this.onChangePassword,
    this.onOpenDeviceStatus,
    this.onOpenSettings,
    this.onReportIssue,
  });

  final AccountViewModel viewModel;
  final VoidCallback onChangeServer;
  final Future<void> Function() onLogout;
  final VoidCallback? onChangePassword;
  final VoidCallback? onOpenDeviceStatus;
  final VoidCallback? onOpenSettings;
  final VoidCallback? onReportIssue;

  @override
  State<AccountPage> createState() => _AccountPageState();
}

class _AccountPageState extends State<AccountPage> {
  bool _isLoggingOut = false;
  String? _errorMessage;

  Future<void> _confirmLogout() async {
    if (_isLoggingOut) {
      return;
    }
    final bool? confirmed = await showDialog<bool>(
      context: context,
      builder: (BuildContext context) => AlertDialog(
        title: const Text('退出登录？'),
        content: const Text('退出后，此设备上保存的登录会话将被清除。'),
        actions: <Widget>[
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('退出'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) {
      return;
    }
    setState(() {
      _isLoggingOut = true;
      _errorMessage = null;
    });
    try {
      await widget.onLogout();
    } catch (error) {
      if (mounted) {
        setState(() {
          _errorMessage = presentationErrorMessage(
            error,
            fallback: '退出请求未完成，请检查网络后重试',
          );
        });
      }
    } finally {
      if (mounted) {
        setState(() => _isLoggingOut = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final ThemeData theme = Theme.of(context);
    final ColorScheme colors = theme.colorScheme;
    return MnemoAdaptiveBuilder(
      builder: (BuildContext context, MnemoWindowClass windowClass) {
        return ListView(
          key: const Key('account-page'),
          padding: MnemoAdaptive.pagePaddingFor(windowClass),
          children: <Widget>[
            MnemoCard(
              tone: MnemoCardTone.elevated,
              child: Row(
                children: <Widget>[
                  CircleAvatar(
                    radius: 30,
                    backgroundColor: colors.primaryContainer,
                    foregroundColor: colors.onPrimaryContainer,
                    child: Text(
                      _accountInitial(widget.viewModel.username),
                      style: theme.textTheme.headlineSmall?.copyWith(
                        color: colors.onPrimaryContainer,
                      ),
                    ),
                  ),
                  const SizedBox(width: MnemoSpacing.md),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: <Widget>[
                        Text(
                          widget.viewModel.username,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: theme.textTheme.titleLarge,
                        ),
                        const SizedBox(height: MnemoSpacing.xxs),
                        Text(
                          widget.viewModel.email?.trim().isNotEmpty == true
                              ? widget.viewModel.email!
                              : widget.viewModel.roleLabel,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: theme.textTheme.bodyMedium?.copyWith(
                            color: colors.onSurfaceVariant,
                          ),
                        ),
                      ],
                    ),
                  ),
                  MnemoStatusPill(
                    label: widget.viewModel.roleLabel,
                    tone: MnemoStatusTone.neutral,
                    compact: true,
                  ),
                ],
              ),
            ),
            if (_errorMessage != null) ...<Widget>[
              const SizedBox(height: MnemoSpacing.md),
              MnemoErrorNotice(
                title: '退出未完成',
                message: _errorMessage!,
                onDismiss: () => setState(() => _errorMessage = null),
              ),
            ],
            const SizedBox(height: MnemoSpacing.xl),
            const MnemoSectionTitle(title: '账户'),
            const SizedBox(height: MnemoSpacing.sm),
            MnemoCard(
              padding: EdgeInsets.zero,
              child: Column(
                children: <Widget>[
                  if (widget.onChangePassword != null)
                    ListTile(
                      leading: const Icon(Icons.password_rounded),
                      title: const Text('修改密码'),
                      trailing: const Icon(Icons.chevron_right_rounded),
                      onTap: widget.onChangePassword,
                    ),
                  if (widget.onChangePassword != null)
                    const Divider(indent: 56),
                  ListTile(
                    key: const Key('account-logout'),
                    leading: const Icon(Icons.logout_rounded),
                    title: Text(_isLoggingOut ? '正在退出' : '退出登录'),
                    enabled: !_isLoggingOut,
                    trailing: _isLoggingOut
                        ? const SizedBox.square(
                            dimension: 20,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.chevron_right_rounded),
                    onTap: _confirmLogout,
                  ),
                ],
              ),
            ),
            const SizedBox(height: MnemoSpacing.xl),
            const MnemoSectionTitle(title: '当前设备'),
            const SizedBox(height: MnemoSpacing.sm),
            MnemoCard(
              padding: EdgeInsets.zero,
              child: Column(
                children: <Widget>[
                  ListTile(
                    leading: const Icon(Icons.storage_rounded),
                    title: Text(widget.viewModel.deviceName),
                    subtitle: Text(widget.viewModel.serverAddress),
                  ),
                  if (widget.viewModel.serverVersion != null) ...<Widget>[
                    const Divider(indent: 56),
                    ListTile(
                      leading: const Icon(Icons.info_outline_rounded),
                      title: const Text('服务端版本'),
                      trailing: Text(widget.viewModel.serverVersion!),
                    ),
                  ],
                  if (widget.onOpenDeviceStatus != null) ...<Widget>[
                    const Divider(indent: 56),
                    ListTile(
                      leading: const Icon(Icons.monitor_heart_outlined),
                      title: const Text('设备状态'),
                      trailing: const Icon(Icons.chevron_right_rounded),
                      onTap: widget.onOpenDeviceStatus,
                    ),
                  ],
                  if (widget.onOpenSettings != null) ...<Widget>[
                    const Divider(indent: 56),
                    ListTile(
                      leading: const Icon(Icons.settings_outlined),
                      title: const Text('设备设置'),
                      trailing: const Icon(Icons.chevron_right_rounded),
                      onTap: widget.onOpenSettings,
                    ),
                  ],
                  const Divider(indent: 56),
                  ListTile(
                    key: const Key('account-change-server'),
                    leading: const Icon(Icons.swap_horiz_rounded),
                    title: const Text('更换设备'),
                    trailing: const Icon(Icons.chevron_right_rounded),
                    onTap: widget.onChangeServer,
                  ),
                ],
              ),
            ),
            if (widget.viewModel.developmentNotice != null ||
                widget.viewModel.clientVersion != null ||
                widget.onReportIssue != null) ...<Widget>[
              const SizedBox(height: MnemoSpacing.xl),
              const MnemoSectionTitle(title: '客户端'),
              const SizedBox(height: MnemoSpacing.sm),
              MnemoCard(
                padding: EdgeInsets.zero,
                child: Column(
                  children: <Widget>[
                    if (widget.viewModel.developmentNotice != null)
                      ListTile(
                        leading: const Icon(Icons.construction_rounded),
                        title: const Text('开发状态'),
                        subtitle: Text(widget.viewModel.developmentNotice!),
                        trailing: const MnemoStatusPill(
                          label: '开发中',
                          tone: MnemoStatusTone.info,
                          compact: true,
                        ),
                      ),
                    if (widget.viewModel.developmentNotice != null &&
                        (widget.viewModel.clientVersion != null ||
                            widget.onReportIssue != null))
                      const Divider(indent: 56),
                    if (widget.viewModel.clientVersion != null)
                      ListTile(
                        leading: const Icon(Icons.apps_rounded),
                        title: const Text('客户端版本'),
                        trailing: Text(widget.viewModel.clientVersion!),
                      ),
                    if (widget.viewModel.clientVersion != null &&
                        widget.onReportIssue != null)
                      const Divider(indent: 56),
                    if (widget.onReportIssue != null)
                      ListTile(
                        key: const Key('account-report-issue'),
                        leading: const Icon(Icons.bug_report_outlined),
                        title: const Text('提交问题反馈'),
                        trailing: const Icon(Icons.open_in_new_rounded),
                        onTap: widget.onReportIssue,
                      ),
                  ],
                ),
              ),
            ],
            const SizedBox(height: MnemoSpacing.xxl),
          ],
        );
      },
    );
  }
}

String _accountInitial(String username) {
  final String value = username.trim();
  if (value.isEmpty) {
    return '?';
  }
  return String.fromCharCode(value.runes.first).toUpperCase();
}
