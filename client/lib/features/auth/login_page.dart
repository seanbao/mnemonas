import 'package:flutter/material.dart';

import '../../design_system/design_system.dart';
import '../shared/presentation_error.dart';

typedef SubmitLogin = Future<void> Function(LoginCredentials credentials);

final class LoginCredentials {
  const LoginCredentials({required this.username, required this.password});

  final String username;
  final String password;
}

class LoginPage extends StatefulWidget {
  const LoginPage({
    super.key,
    required this.serverAddress,
    required this.onLogin,
    required this.onChangeServer,
    this.deviceName,
    this.initialUsername = '',
  });

  final String serverAddress;
  final String? deviceName;
  final String initialUsername;
  final SubmitLogin onLogin;
  final VoidCallback onChangeServer;

  @override
  State<LoginPage> createState() => _LoginPageState();
}

class _LoginPageState extends State<LoginPage> {
  late final TextEditingController _usernameController = TextEditingController(
    text: widget.initialUsername,
  );
  final TextEditingController _passwordController = TextEditingController();
  final GlobalKey<FormState> _formKey = GlobalKey<FormState>();
  bool _passwordVisible = false;
  bool _isSubmitting = false;
  String? _errorMessage;

  @override
  void dispose() {
    _usernameController.dispose();
    _passwordController.dispose();
    super.dispose();
  }

  Future<void> _login() async {
    if (_isSubmitting || !(_formKey.currentState?.validate() ?? false)) {
      return;
    }
    FocusManager.instance.primaryFocus?.unfocus();
    setState(() {
      _isSubmitting = true;
      _errorMessage = null;
    });
    try {
      await widget.onLogin(
        LoginCredentials(
          username: _usernameController.text.trim(),
          password: _passwordController.text,
        ),
      );
    } catch (error) {
      if (mounted) {
        setState(() {
          _errorMessage = presentationErrorMessage(
            error,
            fallback: '登录未完成，请检查用户名、密码和设备状态',
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
                  const AppBrand(size: AppBrandSize.regular, subtitle: '私有存储'),
                  const SizedBox(height: MnemoSpacing.xxl),
                  Text('登录设备', style: theme.textTheme.headlineMedium),
                  const SizedBox(height: MnemoSpacing.md),
                  MnemoCard(
                    tone: MnemoCardTone.muted,
                    child: Row(
                      children: <Widget>[
                        DecoratedBox(
                          decoration: BoxDecoration(
                            color: colors.primaryContainer,
                            borderRadius: BorderRadius.circular(MnemoRadius.sm),
                          ),
                          child: Padding(
                            padding: const EdgeInsets.all(MnemoSpacing.sm),
                            child: Icon(
                              Icons.storage_rounded,
                              color: colors.onPrimaryContainer,
                            ),
                          ),
                        ),
                        const SizedBox(width: MnemoSpacing.sm),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: <Widget>[
                              Text(
                                widget.deviceName?.trim().isNotEmpty == true
                                    ? widget.deviceName!
                                    : 'MnemoNAS 设备',
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: theme.textTheme.titleSmall,
                              ),
                              const SizedBox(height: MnemoSpacing.xxs),
                              Text(
                                widget.serverAddress,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: theme.textTheme.bodySmall?.copyWith(
                                  color: colors.onSurfaceVariant,
                                ),
                              ),
                            ],
                          ),
                        ),
                        TextButton(
                          key: const Key('login-change-server'),
                          onPressed: _isSubmitting
                              ? null
                              : widget.onChangeServer,
                          child: const Text('更换'),
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(height: MnemoSpacing.xl),
                  TextFormField(
                    key: const Key('login-username'),
                    controller: _usernameController,
                    enabled: !_isSubmitting,
                    autofocus: true,
                    textInputAction: TextInputAction.next,
                    autocorrect: false,
                    autofillHints: const <String>[AutofillHints.username],
                    decoration: const InputDecoration(
                      labelText: '用户名',
                      prefixIcon: Icon(Icons.person_outline_rounded),
                    ),
                    validator: (String? value) =>
                        value == null || value.trim().isEmpty ? '请输入用户名' : null,
                    onChanged: (_) {
                      if (_errorMessage != null) {
                        setState(() => _errorMessage = null);
                      }
                    },
                  ),
                  const SizedBox(height: MnemoSpacing.md),
                  TextFormField(
                    key: const Key('login-password'),
                    controller: _passwordController,
                    enabled: !_isSubmitting,
                    obscureText: !_passwordVisible,
                    textInputAction: TextInputAction.done,
                    autocorrect: false,
                    enableSuggestions: false,
                    autofillHints: const <String>[AutofillHints.password],
                    decoration: InputDecoration(
                      labelText: '密码',
                      prefixIcon: const Icon(Icons.lock_outline_rounded),
                      suffixIcon: IconButton(
                        key: const Key('login-password-visibility'),
                        onPressed: () => setState(
                          () => _passwordVisible = !_passwordVisible,
                        ),
                        tooltip: _passwordVisible ? '隐藏密码' : '显示密码',
                        icon: Icon(
                          _passwordVisible
                              ? Icons.visibility_off_outlined
                              : Icons.visibility_outlined,
                        ),
                      ),
                    ),
                    validator: (String? value) =>
                        value == null || value.isEmpty ? '请输入密码' : null,
                    onFieldSubmitted: (_) => _login(),
                    onChanged: (_) {
                      if (_errorMessage != null) {
                        setState(() => _errorMessage = null);
                      }
                    },
                  ),
                  if (_errorMessage != null) ...<Widget>[
                    const SizedBox(height: MnemoSpacing.md),
                    MnemoErrorNotice(
                      title: '登录失败',
                      message: _errorMessage!,
                      onRetry: _login,
                    ),
                  ],
                  const SizedBox(height: MnemoSpacing.xl),
                  MnemoPrimaryButton(
                    key: const Key('login-submit'),
                    label: _isSubmitting ? '正在登录' : '登录',
                    icon: Icons.login_rounded,
                    isLoading: _isSubmitting,
                    expand: true,
                    onPressed: _isSubmitting ? null : _login,
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
