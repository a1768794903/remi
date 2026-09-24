import 'package:flutter/material.dart';

import 'services/wearable_connection.dart';

void main() => runApp(const RemiApp());

class RemiApp extends StatefulWidget {
  const RemiApp({super.key});

  @override
  State<RemiApp> createState() => _RemiAppState();
}

class _RemiAppState extends State<RemiApp> {
  final wearable = WearableConnection();

  @override
  void dispose() {
    wearable.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Remi',
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(seedColor: const Color(0xFF746EAD)),
        useMaterial3: true,
      ),
      home: Scaffold(
        appBar: AppBar(title: const Text('Remi')),
        body: SafeArea(
          child: AnimatedBuilder(
            animation: wearable,
            builder: (context, _) => ListView(
              padding: const EdgeInsets.all(24),
              children: [
                Text('Today', style: Theme.of(context).textTheme.headlineLarge),
                const SizedBox(height: 24),
                Card(
                  child: Padding(
                    padding: const EdgeInsets.all(20),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          'Remi Wearable',
                          style: Theme.of(context).textTheme.titleLarge,
                        ),
                        const SizedBox(height: 12),
                        Text('Status: ${wearable.status.name}'),
                        Text(
                          'Battery: ${wearable.batteryLevel == null ? '—' : '${wearable.batteryLevel}%'}',
                        ),
                        Text(
                          'Firmware: ${wearable.firmwareVersion?.isNotEmpty == true ? wearable.firmwareVersion : '—'}',
                        ),
                        const SizedBox(height: 16),
                        if (wearable.status == WearableStatus.connected)
                          FilledButton(
                            onPressed: wearable.disconnect,
                            child: const Text('Disconnect'),
                          )
                        else
                          FilledButton(
                            onPressed:
                                wearable.status == WearableStatus.scanning ||
                                    wearable.status == WearableStatus.connecting
                                ? null
                                : wearable.scan,
                            child: Text(
                              wearable.status == WearableStatus.scanning
                                  ? 'Scanning…'
                                  : 'Connect',
                            ),
                          ),
                      ],
                    ),
                  ),
                ),
                if (wearable.error != null)
                  Padding(
                    padding: const EdgeInsets.only(top: 12),
                    child: Text(
                      wearable.error!,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.error,
                      ),
                    ),
                  ),
                for (final result in wearable.discovered)
                  ListTile(
                    title: Text(
                      result.device.platformName.isEmpty
                          ? 'Remi Wearable'
                          : result.device.platformName,
                    ),
                    subtitle: Text(result.device.remoteId.toString()),
                    trailing: const Icon(Icons.chevron_right),
                    onTap: () => wearable.connect(result.device),
                  ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
