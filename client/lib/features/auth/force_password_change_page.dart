import 'dart:convert';

import 'package:flutter/material.dart';

import '../../design_system/design_system.dart';
import '../shared/presentation_error.dart';

typedef SubmitRequiredPasswordChange =
    Future<void> Function(RequiredPasswordChange change);

final class RequiredPasswordChange {
  const RequiredPasswordChange({
    required this.currentPassword,
    required this.newPassword,
  });

  final String currentPassword;
  final String newPassword;
}

class ForcePasswordChangePage extends StatefulWidget {
  const ForcePasswordChangePage({
    super.key,
    required this.username,
    required this.onSubmit,
    required this.onLogout,
  });

  final String username;
  final SubmitRequiredPasswordChange onSubmit;
  final VoidCallback onLogout;

  @override
  State<ForcePasswordChangePage> createState() =>
      _ForcePasswordChangePageState();
}

class _ForcePasswordChangePageState extends State<ForcePasswordChangePage> {
  final TextEditingController _currentPasswordController =
      TextEditingController();
  final TextEditingController _newPasswordController = TextEditingController();
  final TextEditingController _confirmPasswordController =
      TextEditingController();
  final GlobalKey<FormState> _formKey = GlobalKey<FormState>();
  bool _passwordsVisible = false;
  bool _isSubmitting = false;
  String? _errorMessage;

  @override
  void dispose() {
    _currentPasswordController.dispose();
    _newPasswordController.dispose();
    _confirmPasswordController.dispose();
    super.dispose();
  }

  String? _validateNewPassword(String? value) {
    final String password = value ?? '';
    if (password.trim().isEmpty || utf8.encode(password).length < 8) {
      return '新密码至少包含 8 个 UTF-8 字节，且不能只有空白字符';
    }
    if (utf8.encode(password).length > 72) {
      return '新密码不能超过 72 个 UTF-8 字节';
    }
    if (password == _currentPasswordController.text) {
      return '新密码不能与当前密码相同';
    }
    return null;
  }

  Future<void> _submit() async {
    if (_isSubmitting || !(_formKey.currentState?.validate() ?? false)) {
      return;
    }
    FocusManager.instance.primaryFocus?.unfocus();
    setState(() {
      _isSubmitting = true;
      _errorMessage = null;
    });
    try {
      await widget.onSubmit(
        RequiredPasswordChange(
          currentPassword: _currentPasswordController.text,
          newPassword: _newPasswordController.text,
        ),
      );
    } catch (error) {
      if (mounted) {
        setState(() {
          _errorMessage = presentationErrorMessage(
            error,
            fallback: '密码修改未完成，请检查当前密码后重试',
          );
        });
      }
    } finally {
      if (mounted) {
        setState(() => _isSubmitting = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final ThemeData theme = Theme.of(context);
    final ColorScheme colors = theme.colorScheme;
    return Scaffold(
      body: MnemoContentFrame(
        maxWidth: MnemoBreakpoint.readingMax,
        alignment: Alignment.center,
        child: SingleChildScrollView(
          child: AutofillGroup(
            child: Form(
              key: _formKey,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: <Widget>[
                  const AppBrand(size: AppBrandSize.regular, subtitle: '账户安全'),
                  const SizedBox(height: MnemoSpacing.xxl),
                  Text('设置新密码', style: theme.textTheme.headlineMedium),
                  const SizedBox(height: MnemoSpacing.xs),
                  Text(
                    '${widget.username} 使用的是初始密码。继续使用设备前，需要先设置新密码。',
                    style: theme.textTheme.bodyLarge?.copyWith(
                      color: colors.onSurfaceVariant,
                    ),
                  ),
                  const SizedBox(height: MnemoSpacing.md),
                  MnemoCard(
                    tone: MnemoCardTone.brandTint,
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: <Widget>[
                        Icon(
                          Icons.info_outline_rounded,
                          color: colors.onPrimaryContainer,
                        ),
                        const SizedBox(width: MnemoSpacing.sm),
                        Expanded(
                          child: Text(
                            '修改成功后，当前会话会结束。请使用新密码重新登录。',
                            style: theme.textTheme.bodyMedium?.copyWith(
                              color: colors.onPrimaryContainer,
                            ),
                          ),
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(height: MnemoSpacing.xl),
                  TextFormField(
                    key: const Key('password-change-current'),
                    controller: _currentPasswordController,
                    enabled: !_isSubmitting,
                    obscureText: !_passwordsVisible,
                    textInputAction: TextInputAction.next,
                    autocorrect: false,
                    enableSuggestions: false,
                    autofillHints: const <String>[AutofillHints.password],
                    decoration: const InputDecoration(
                      labelText: '当前密码',
                      prefixIcon: Icon(Icons.lock_outline_rounded),
                    ),
                    validator: (String? value) =>
                        value == null || value.isEmpty ? '请输入当前密码' : null,
                  ),
                  const SizedBox(height: MnemoSpacing.md),
                  TextFormField(
                    key: const Key('password-change-new'),
                    controller: _newPasswordController,
                    enabled: !_isSubmitting,
                    obscureText: !_passwordsVisible,
                    textInputAction: TextInputAction.next,
                    autocorrect: false,
                    enableSuggestions: false,
                    autofillHints: const <String>[AutofillHints.newPassword],
                    decoration: const InputDecoration(
                      labelText: '新密码',
                      helperText: '8–72 个 UTF-8 字节，不能只包含空白字符',
                      prefixIcon: Icon(Icons.password_rounded),
                    ),
                    validator: _validateNewPassword,
                  ),
                  const SizedBox(height: MnemoSpacing.md),
                  TextFormField(
                    key: const Key('password-change-confirm'),
                    controller: _confirmPasswordController,
                    enabled: !_isSubmitting,
                    obscureText: !_passwordsVisible,
                    textInputAction: TextInputAction.done,
                    autocorrect: false,
                    enableSuggestions: false,
                    autofillHints: const <String>[AutofillHints.newPassword],
                    decoration: InputDecoration(
                      labelText: '确认新密码',
                      prefixIcon: const Icon(Icons.verified_user_outlined),
                      suffixIcon: IconButton(
                        key: const Key('password-change-visibility'),
                        onPressed: () => setState(
                          () => _passwordsVisible = !_passwordsVisible,
                        ),
                        tooltip: _passwordsVisible ? '隐藏密码' : '显示密码',
                        icon: Icon(
                          _passwordsVisible
                              ? Icons.visibility_off_outlined
                              : Icons.visibility_outlined,
                        ),
                      ),
                    ),
                    validator: (String? value) =>
                        value != _newPasswordController.text
                        ? '两次输入的密码不一致'
                        : null,
                    onFieldSubmitted: (_) => _submit(),
                  ),
                  if (_errorMessage != null) ...<Widget>[
                    const SizedBox(height: MnemoSpacing.md),
                    MnemoErrorNotice(
                      title: '修改失败',
                      message: _errorMessage!,
                      onRetry: _submit,
                    ),
                  ],
                  const SizedBox(height: MnemoSpacing.xl),
                  MnemoPrimaryButton(
                    key: const Key('password-change-submit'),
                    label: _isSubmitting ? '正在修改' : '修改密码',
                    icon: Icons.check_rounded,
                    isLoading: _isSubmitting,
                    expand: true,
                    onPressed: _isSubmitting ? null : _submit,
                  ),
                  const SizedBox(height: MnemoSpacing.sm),
                  TextButton(
                    onPressed: _isSubmitting ? null : widget.onLogout,
                    child: const Text('退出登录'),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
