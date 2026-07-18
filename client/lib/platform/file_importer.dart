import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

typedef FileImportProgressCallback = void Function(int transferred, int? total);

final class FileImportSource {
  const FileImportSource({
    required this.uri,
    required this.displayName,
    required this.mimeType,
    required this.size,
  });

  factory FileImportSource.fromPlatformValue(Object? value) {
    if (value is! Map<Object?, Object?>) {
      throw const FormatException('Expected file import metadata');
    }

    final uri = _requireContentUri(value['uri']);
    final displayName = value['displayName'];
    if (displayName is! String ||
        displayName.isEmpty ||
        displayName == '.' ||
        displayName == '..' ||
        _unsafeDisplayName.hasMatch(displayName)) {
      throw const FormatException('Invalid imported file display name');
    }

    final mimeTypeValue = value['mimeType'];
    if (mimeTypeValue != null &&
        (mimeTypeValue is! String ||
            !_mimeTypePattern.hasMatch(mimeTypeValue))) {
      throw const FormatException('Invalid imported file MIME type');
    }

    final sizeValue = value['size'];
    if (sizeValue != null && (sizeValue is! int || sizeValue < 0)) {
      throw const FormatException('Invalid imported file size');
    }

    return FileImportSource(
      uri: uri,
      displayName: displayName,
      mimeType: mimeTypeValue as String?,
      size: sizeValue as int?,
    );
  }

  final String uri;
  final String displayName;
  final String? mimeType;
  final int? size;
}

abstract final class FileImporter {
  static const MethodChannel _channel = MethodChannel(
    'com.mnemonas.app/file_import',
  );
  static final Set<String> _activeOperationIds = <String>{};
  static final Map<String, FileImportProgressCallback> _progressCallbacks =
      <String, FileImportProgressCallback>{};
  static bool _handlerInstalled = false;

  static Future<List<FileImportSource>> pickDocuments() async {
    _requireAndroid();
    final value = await _channel.invokeMethod<Object?>('pickDocuments');
    if (value is! List<Object?>) {
      throw const FormatException('Expected a file import metadata list');
    }

    final sources = <FileImportSource>[];
    final uris = <String>{};
    for (final item in value) {
      final source = FileImportSource.fromPlatformValue(item);
      if (!uris.add(source.uri)) {
        throw const FormatException('Duplicate imported file URI');
      }
      sources.add(source);
    }
    return List<FileImportSource>.unmodifiable(sources);
  }

  static Future<File> copyDocumentToFile({
    required String uri,
    required String destinationPath,
    required String operationId,
    int? expectedLength,
    FileImportProgressCallback? onProgress,
  }) async {
    _requireAndroid();
    _ensureProgressHandler();
    final normalizedUri = _requireContentUri(uri);
    final normalizedOperationId = _requireOperationId(operationId);
    if (destinationPath.isEmpty || !File(destinationPath).isAbsolute) {
      throw ArgumentError.value(
        destinationPath,
        'destinationPath',
        'An absolute destination path is required',
      );
    }
    if (expectedLength != null && expectedLength < 0) {
      throw ArgumentError.value(
        expectedLength,
        'expectedLength',
        'Expected length cannot be negative',
      );
    }
    if (!_activeOperationIds.add(normalizedOperationId)) {
      throw StateError('An import operation with this ID is already active');
    }
    if (onProgress != null) {
      _progressCallbacks[normalizedOperationId] = onProgress;
    }

    try {
      await _channel.invokeMethod<void>('copyDocumentToFile', {
        'operationId': normalizedOperationId,
        'uri': normalizedUri,
        'destinationPath': destinationPath,
        'expectedLength': expectedLength,
      });
      return File(destinationPath);
    } finally {
      _progressCallbacks.remove(normalizedOperationId);
      _activeOperationIds.remove(normalizedOperationId);
    }
  }

  static Future<void> cancelCopy(String operationId) async {
    _requireAndroid();
    await _channel.invokeMethod<void>('cancelCopy', {
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
        'Unknown file import callback: ${call.method}',
      );
    }
    final values = call.arguments;
    if (values is! Map<Object?, Object?>) {
      throw const FormatException('Invalid file import progress payload');
    }
    final operationId = _requireOperationId(values['operationId']);
    final transferred = values['transferred'];
    final total = values['total'];
    if (transferred is! int ||
        transferred < 0 ||
        (total != null && (total is! int || total < transferred))) {
      throw const FormatException('Invalid file import progress values');
    }
    _progressCallbacks[operationId]?.call(transferred, total as int?);
    return null;
  }

  static void _requireAndroid() {
    if (defaultTargetPlatform != TargetPlatform.android) {
      throw UnsupportedError(
        'Storage Access Framework imports are available only on Android',
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

final RegExp _unsafeDisplayName = RegExp(r'[\u0000-\u001f\u007f/\\]');
final RegExp _mimeTypePattern = RegExp(
  r'^[A-Za-z0-9!#$&^_.+-]+/[A-Za-z0-9!#$&^_.+-]+$',
);
final RegExp _operationIdPattern = RegExp(
  r'^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$',
);
