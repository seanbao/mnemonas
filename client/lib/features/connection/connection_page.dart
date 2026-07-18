import 'package:flutter/material.dart';

import '../../core/server/server_endpoint.dart';
import '../../design_system/design_system.dart';
import '../shared/presentation_error.dart';

typedef ValidateServerConnection =
    Future<ServerConnectionInfo> Function(ServerEndpoint endpoint);

final class ServerConnectionInfo {
  const ServerConnectionInfo({
    required this.endpoint,
    this.deviceName,
    this.serverVersion,
  });

  final ServerEndpoint endpoint;
  final String? deviceName;
  final String? serverVersion;
}

class ConnectionPage extends StatefulWidget {
  const ConnectionPage({
    super.key,
    required this.onValidate,
    this.initialAddress = '',
  });

  final ValidateServerConnection onValidate;
  final String initialAddress;

  @override
  State<ConnectionPage> createState() => _ConnectionPageState();
}

class _ConnectionPageState extends State<ConnectionPage> {
  late final TextEditingController _addressController = TextEditingController(
    text: widget.initialAddress,
  );
  final GlobalKey<FormState> _formKey = GlobalKey<FormState>();
  bool _isValidating = false;
  String? _errorMessage;
  ServerEndpoint? _parsedEndpoint;

  @override
  void initState() {
    super.initState();
    _addressController.addListener(_handleAddressChanged);
    _refreshParsedEndpoint();
  }

  @override
  void didUpdateWidget(covariant ConnectionPage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.initialAddress != widget.initialAddress &&
        widget.initialAddress != _addressController.text) {
      _addressController.text = widget.initialAddress;
    }
  }

  @override
  void dispose() {
    _addressController
      ..removeListener(_handleAddressChanged)
      ..dispose();
    super.dispose();
  }

  void _handleAddressChanged() {
    if (_errorMessage != null) {
      setState(() => _errorMessage = null);
    }
    _refreshParsedEndpoint();
  }

  void _refreshParsedEndpoint() {
    ServerEndpoint? endpoint;
    try {
      endpoint = ServerEndpoint.parse(_addressController.text);
    } on FormatException {
      endpoint = null;
    }
    if (endpoint != _parsedEndpoint && mounted) {
      setState(() => _parsedEndpoint = endpoint);
    } else {
      _parsedEndpoint = endpoint;
    }
  }

  String? _validateAddress(String? value) {
    try {
      ServerEndpoint.parse(value ?? '');
      return null;
    } on FormatException catch (error) {
      return presentationErrorMessage(error);
    }
  }

  Future<void> _connect() async {
    if (_isValidating || !(_formKey.currentState?.validate() ?? false)) {
      return;
    }
    FocusManager.instance.primaryFocus?.unfocus();
    final endpoint = ServerEndpoint.parse(_addressController.text);
    setState(() {
      _isValidating = true;
      _errorMessage = null;
    });
    try {
      await widget.onValidate(endpoint);
    } catch (error) {
      if (mounted) {
        setState(() {
          _errorMessage = presentationErrorMessage(
            error,
            fallback: '设备验证失败，请确认地址和服务状态',
          );
        });
      }
    } finally {
      if (mounted) {
        setState(() => _isValidating = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final ColorScheme colors = Theme.of(context).colorScheme;
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
                  const Align(
                    alignment: Alignment.centerLeft,
                    child: AppBrand(
                      size: AppBrandSize.hero,
                      subtitle: '连接私有存储',
                    ),
                  ),
                  const SizedBox(height: MnemoSpacing.xxl),
                  Text(
                    '连接到设备',
                    style: Theme.of(context).textTheme.headlineMedium,
                  ),
                  const SizedBox(height: MnemoSpacing.xs),
                  Text(
                    '输入 MnemoNAS 的完整访问地址。连接前会验证设备是否可用。',
                    style: Theme.of(context).textTheme.bodyLarge?.copyWith(
                      color: colors.onSurfaceVariant,
                    ),
                  ),
                  const SizedBox(height: MnemoSpacing.xl),
                  TextFormField(
                    key: const Key('connection-address-field'),
                    controller: _addressController,
                    enabled: !_isValidating,
                    autofocus: true,
                    keyboardType: TextInputType.url,
                    textInputAction: TextInputAction.done,
                    autocorrect: false,
                    enableSuggestions: false,
                    autofillHints: const <String>[AutofillHints.url],
                    decoration: const InputDecoration(
                      labelText: '设备地址',
                      hintText: 'https://nas.example.com',
                      prefixIcon: Icon(Icons.dns_outlined),
                    ),
                    validator: _validateAddress,
                    onFieldSubmitted: (_) => _connect(),
                  ),
                  const SizedBox(height: MnemoSpacing.sm),
                  _TransportNotice(endpoint: _parsedEndpoint),
                  if (_errorMessage != null) ...<Widget>[
                    const SizedBox(height: MnemoSpacing.md),
                    MnemoErrorNotice(
                      title: '无法连接',
                      message: _errorMessage!,
                      onRetry: _connect,
                    ),
                  ],
                  const SizedBox(height: MnemoSpacing.xl),
                  MnemoPrimaryButton(
                    key: const Key('connection-submit'),
                    label: _isValidating ? '正在验证设备' : '验证并继续',
                    icon: Icons.arrow_forward_rounded,
                    isLoading: _isValidating,
                    expand: true,
                    onPressed: _isValidating ? null : _connect,
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

class _TransportNotice extends StatelessWidget {
  const _TransportNotice({required this.endpoint});

  final ServerEndpoint? endpoint;

  @override
  Widget build(BuildContext context) {
    final bool usesLanHttp = endpoint?.usesLanHttp ?? false;
    final MnemoSemanticColors semantic = context.mnemoColors;
    final Color foreground = usesLanHttp
        ? semantic.onWarningContainer
        : Theme.of(context).colorScheme.onSurfaceVariant;
    return Semantics(
      container: true,
      child: DecoratedBox(
        decoration: BoxDecoration(
          color: usesLanHttp
              ? semantic.warningContainer
              : context.mnemoColors.surfaceMuted,
          borderRadius: BorderRadius.circular(MnemoRadius.sm),
        ),
        child: Padding(
          padding: const EdgeInsets.all(MnemoSpacing.sm),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: <Widget>[
              Icon(
                usesLanHttp
                    ? Icons.warning_amber_rounded
                    : Icons.shield_outlined,
                size: 20,
                color: foreground,
              ),
              const SizedBox(width: MnemoSpacing.xs),
              Expanded(
                child: Text(
                  usesLanHttp
                      ? '当前使用局域网 HTTP，传输内容不会加密。离开可信本地网络前应配置 HTTPS。'
                      : '建议使用 HTTPS。HTTP 仅允许本机或局域网地址。',
                  style: Theme.of(
                    context,
                  ).textTheme.bodySmall?.copyWith(color: foreground),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
