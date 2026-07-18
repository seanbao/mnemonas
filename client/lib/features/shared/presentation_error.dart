import '../../core/network/api_error.dart';

String presentationErrorMessage(
  Object error, {
  String fallback = '操作未完成，请稍后重试',
}) {
  if (error is FormatException) {
    return switch (error.message) {
      'Server URL is required' => '请输入设备地址',
      'Enter a complete HTTP or HTTPS URL' => '请输入完整的 HTTP 或 HTTPS 地址',
      'Only HTTP and HTTPS server URLs are allowed' => '设备地址仅支持 HTTP 或 HTTPS',
      'Server URL must include a host' => '设备地址缺少主机名或 IP',
      'Server URL must not include credentials' => '设备地址不能包含用户名或密码',
      'Server URL must not include a query' => '设备地址不能包含查询参数',
      'Server URL must not include a fragment' => '设备地址不能包含片段标识',
      'Server URL must not include a path' => '设备地址不能包含路径',
      'Public servers require HTTPS; HTTP is limited to local networks' =>
        '公网设备必须使用 HTTPS；HTTP 仅适用于本地网络',
      _ => error.message.toString(),
    };
  }
  if (error is ApiException) {
    final int seconds = error.retryAfter?.inSeconds ?? 0;
    return switch (error.code) {
      'LOGIN_RATE_LIMITED' =>
        seconds > 0 ? '登录尝试过于频繁，请在 $seconds 秒后再试' : '登录尝试过于频繁，请稍后再试',
      'PASSWORD_TOO_SHORT' => '新密码至少包含 8 个 UTF-8 字节，且不能只有空白字符',
      'PASSWORD_TOO_LONG' => '新密码不能超过 72 个 UTF-8 字节',
      'PASSWORD_UNCHANGED' => '新密码不能与当前密码相同',
      'UNAUTHORIZED' => '用户名或密码不正确',
      _ => switch (error.kind) {
        ApiFailureKind.connection => '无法连接设备，请确认地址和网络状态',
        ApiFailureKind.timeout => '设备响应超时，请检查网络后重试',
        ApiFailureKind.cancelled => '操作已取消',
        ApiFailureKind.invalidResponse => '设备返回了无法识别的响应',
        ApiFailureKind.local => error.message,
        ApiFailureKind.response =>
          error.statusCode == 401
              ? '用户名或密码不正确'
              : (error.message.trim().isEmpty ? fallback : error.message),
      },
    };
  }
  return fallback;
}
