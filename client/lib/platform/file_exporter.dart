import 'dart:io';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

typedef FileExportProgressCallback = void Function(int transferred, int? total);

final class FileExportTarget {
  const FileExportTarget._({this.path, this.contentUri});

  factory FileExportTarget.path(String path) => FileExportTarget._(path: path);

  factory FileExportTarget.contentUri(String uri) {
    return FileExportTarget._(contentUri: _requireContentUri(uri));
  }

  final String? path;
  final String? contentUri;

  bool get requiresPrivateStaging => contentUri != null;
}

abstract final class FileExporter {
  static const MethodChannel _channel = MethodChannel(
    'com.mnemonas.app/file_export',
  );
  static final Set<String> _activeOperationIds = <String>{};
  static final Map<String, FileExportProgressCallback> _progressCallbacks =
      <String, FileExportProgressCallback>{};
  static bool _handlerInstalled = false;

  static Future<FileExportTarget?> chooseTarget({
    required String suggestedName,
    required String mimeType,
  }) async {
    if (defaultTargetPlatform == TargetPlatform.android) {
      final uri = await _channel.invokeMethod<String>('createDocument', {
        'suggestedName': suggestedName,
        'mimeType': mimeType,
      });
      return uri == null
          ? null
          : FileExportTarget.contentUri(_requireContentUri(uri));
    }

    final location = await getSaveLocation(suggestedName: suggestedName);
    return location == null ? null : FileExportTarget.path(location.path);
  }

  static Future<void> exportStagedFile({
    required String sourcePath,
    required FileExportTarget target,
    required String operationId,
    FileExportProgressCallback? onProgress,
  }) async {
    final uri = target.contentUri;
    if (defaultTargetPlatform != TargetPlatform.android || uri == null) {
      throw StateError('The export target does not require native staging');
    }
    if (sourcePath.isEmpty || !File(sourcePath).isAbsolute) {
      throw ArgumentError.value(
        sourcePath,
        'sourcePath',
        'An absolute staged source path is required',
      );
    }
    _ensureProgressHandler();
    final normalizedOperationId = _requireOperationId(operationId);
    if (!_activeOperationIds.add(normalizedOperationId)) {
      throw StateError('An export operation with this ID is already active');
    }
    if (onProgress != null) {
      _progressCallbacks[normalizedOperationId] = onProgress;
    }

    try {
      await _channel.invokeMethod<void>('copyToDocument', {
        'operationId': normalizedOperationId,
        'sourcePath': sourcePath,
        'targetUri': uri,
      });
    } finally {
      _progressCallbacks.remove(normalizedOperationId);
      _activeOperationIds.remove(normalizedOperationId);
    }
  }

  static Future<void> cancelExport(String operationId) async {
    _requireAndroid();
    await _channel.invokeMethod<void>('cancelExport', {
      'operationId': _requireOperationId(operationId),
    });
  }

  static void _ensureProgressHandler() {
    if (_handlerInstalled) {
      return;
    }
    _channel.setMethodCallHandler(_handlePlatformCall);
    _handlerInstalled = true;
  }

  static Future<Object?> _handlePlatformCall(MethodCall call) async {
    if (call.method != 'copyProgress') {
      throw MissingPluginException(
        'Unknown file export callback: ${call.method}',
      );
    }
    final values = call.arguments;
    if (values is! Map<Object?, Object?>) {
      throw const FormatException('Invalid file export progress payload');
    }
    final operationId = _requireOperationId(values['operationId']);
    final transferred = values['transferred'];
    final total = values['total'];
    if (transferred is! int ||
        transferred < 0 ||
        (total != null && (total is! int || total < transferred))) {
      throw const FormatException('Invalid file export progress values');
    }
    _progressCallbacks[operationId]?.call(transferred, total as int?);
    return null;
  }

  static void _requireAndroid() {
    if (defaultTargetPlatform != TargetPlatform.android) {
      throw UnsupportedError(
        'Storage Access Framework exports are available only on Android',
      );
    }
  }
}

String _requireOperationId(Object? value) {
  if (value is! String || !_operationIdPattern.hasMatch(value)) {
    throw const FormatException('Invalid storage operation ID');
  }
  return value;
}

String _requireContentUri(Object? value) {
  if (value is! String || value.isEmpty) {
    throw const FormatException('A content URI is required');
  }
  final parsed = Uri.tryParse(value);
  if (parsed == null ||
      parsed.scheme != 'content' ||
      !parsed.hasAuthority ||
      parsed.authority.isEmpty ||
      parsed.hasFragment) {
    throw const FormatException('Invalid content URI');
  }
  return parsed.toString();
}

final RegExp _operationIdPattern = RegExp(
  r'^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$',
);
