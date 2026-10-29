import { type ApiError } from "@/common/api";
import { authenticateByPassword, initiateQuickConnect, type JellyfinAuthResult, type JellyfinQuickConnect, pollQuickConnect } from "@/jellyfin/api";
import { JellyfinAuthContext, type JellyfinAuthOnAdd } from "@/jellyfin/JellyfinAuthContext";
import { Button, Code, Divider, Flex, Kbd, Loader, Modal, PasswordInput, SegmentedControl, Text, TextInput, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { type PropsWithChildren, useCallback, useEffect, useRef, useState } from "react";

const quickConnectPollInterval = 3000;

type Method = "password" | "quickConnect";

export function JellyfinAuthProvider({ children }: PropsWithChildren) {
	const [opened, setOpened] = useState(false);
	const [method, setMethod] = useState<Method>("password");
	const [url, setUrl] = useState("");
	const [username, setUsername] = useState("");
	const [password, setPassword] = useState("");
	const [pending, setPending] = useState(false);
	const [quickConnect, setQuickConnect] = useState<JellyfinQuickConnect | null>(null);

	const onAddRef = useRef<JellyfinAuthOnAdd | null>(null);

	const reset = useCallback(() => {
		setOpened(false);
		setMethod("password");
		setUsername("");
		setPassword("");
		setPending(false);
		setQuickConnect(null);
	}, []);

	const cancel = useCallback(() => {
		reset();
		notifications.show({
			color: "red",
			title: "Jellyfin sign-in cancelled",
			message: "",
		});
	}, [reset]);

	const handleError = useCallback((error: ApiError) => {
		setPending(false);
		setQuickConnect(null);
		notifications.show({
			color: "red",
			title: "Jellyfin sign-in failed",
			message: error.message,
		});
	}, []);

	const complete = useCallback(
		(result: JellyfinAuthResult) => {
			if (onAddRef.current) {
				onAddRef.current(result);
			}
			reset();
			notifications.show({
				color: "green",
				title: "Jellyfin sign-in successful!",
				message: `Server "${result.serverName}" added for user "${result.userName}"`,
			});
		},
		[reset],
	);

	const auth = useCallback((onAdd: JellyfinAuthOnAdd) => {
		onAddRef.current = onAdd;
		setOpened(true);
	}, []);

	const signInWithPassword = useCallback(async () => {
		setPending(true);
		try {
			complete(await authenticateByPassword(url.trim(), username.trim(), password));
		} catch (error) {
			handleError(error as ApiError);
		}
	}, [url, username, password, complete, handleError]);

	const startQuickConnect = useCallback(async () => {
		setPending(true);
		try {
			setQuickConnect(await initiateQuickConnect(url.trim()));
		} catch (error) {
			handleError(error as ApiError);
		}
	}, [url, handleError]);

	useEffect(() => {
		if (!quickConnect) {
			return;
		}
		let stopped = false;
		const interval = window.setInterval(async () => {
			try {
				const result = await pollQuickConnect(quickConnect);
				if (!stopped && result.authenticated) {
					stopped = true;
					complete(result);
				}
			} catch (error) {
				if (!stopped) {
					stopped = true;
					handleError(error as ApiError);
				}
			}
		}, quickConnectPollInterval);
		return () => {
			stopped = true;
			clearInterval(interval);
		};
	}, [quickConnect, complete, handleError]);

	const urlValid = (/^https?:\/\/\S+$/).test(url.trim());

	const modal = opened && (
		<Modal autoFocus centered closeOnClickOutside={false} closeOnEscape={false} onClose={cancel} opened padding="lg" size="lg" trapFocus withCloseButton={false}>
			<Flex direction="column" gap="md">
				<Flex direction="column" gap={4}>
					<Text fw={500}>Add a Jellyfin Server</Text>
					<Text c="dimmed" size="sm">
						Sign in with your Jellyfin username and password, or authorise this app from another signed-in Jellyfin client using Quick Connect.
					</Text>
				</Flex>
				<Divider />
				<TextInput
					data-autofocus
					disabled={pending}
					label="Server URL"
					onChange={(e) => {
						setUrl(e.currentTarget.value);
					}}
					placeholder="e.g. http://192.168.1.10:8096"
					value={url}
				/>
				<SegmentedControl
					data={[
						{ value: "password", label: "Username & Password" },
						{ value: "quickConnect", label: "Quick Connect" },
					]}
					disabled={pending}
					onChange={(value) => {
						setMethod(value as Method);
					}}
					value={method}
				/>
				{method === "password" ? (
					<Flex
						component="form"
						direction="column"
						gap="md"
						onSubmit={(e) => {
							e.preventDefault();
							void signInWithPassword();
						}}
					>
						<TextInput
							autoComplete="username"
							disabled={pending}
							label="Username"
							onChange={(e) => {
								setUsername(e.currentTarget.value);
							}}
							value={username}
						/>
						<PasswordInput
							autoComplete="current-password"
							disabled={pending}
							label="Password"
							onChange={(e) => {
								setPassword(e.currentTarget.value);
							}}
							value={password}
						/>
						<Button disabled={!urlValid || !username.trim()} loading={pending} type="submit">
							Sign In
						</Button>
					</Flex>
				) : quickConnect ? (
					<Flex align="center" direction="column" gap="sm" style={{ textAlign: "center" }}>
						<Text size="sm">Enter this code in a signed-in Jellyfin client under:</Text>
						<Kbd size="md">User menu &gt; Settings &gt; Quick Connect</Kbd>
						<Title order={1}>
							<Code fz="inherit">{quickConnect.code}</Code>
						</Title>
						<Flex align="center" gap="sm">
							<Loader size="xs" />
							<Text c="dimmed" size="sm">
								Waiting for authorisation...
							</Text>
						</Flex>
					</Flex>
				) : (
					<Flex direction="column" gap="md">
						<Text c="dimmed" size="sm">
							Quick Connect must be enabled on the server (Dashboard &gt; General &gt; Quick Connect).
						</Text>
						<Button
							disabled={!urlValid}
							loading={pending}
							onClick={() => {
								void startQuickConnect();
							}}
						>
							Get Code
						</Button>
					</Flex>
				)}
				<Button onClick={cancel} variant="subtle">
					Cancel
				</Button>
			</Flex>
		</Modal>
	);

	return (
		<JellyfinAuthContext.Provider value={auth}>
			{children}
			{modal}
		</JellyfinAuthContext.Provider>
	);
}
