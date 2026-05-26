import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CameraDialog } from "./CameraDialog";

afterEach(cleanup);

describe("CameraDialog — add mode", () => {
  it("renders an empty form ready for a new camera", () => {
    render(
      <CameraDialog
        mode="add"
        onSubmit={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    expect(screen.getByLabelText(/label/i)).toHaveValue("");
    expect(screen.getByLabelText(/rtsp url/i)).toHaveValue("");
    expect(screen.getByLabelText(/lpr/i)).not.toBeChecked();
    expect(screen.getByRole("heading", { name: /add camera/i })).toBeInTheDocument();
  });

  it("pre-fills the RTSP URL with a canonical template when prefillIp is set (issue #3 shortcut)", () => {
    render(
      <CameraDialog
        mode="add"
        prefillIp="192.168.1.42"
        onSubmit={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    const rtspInput = screen.getByLabelText(/rtsp url/i) as HTMLInputElement;
    expect(rtspInput.value).toContain("192.168.1.42");
    expect(rtspInput.value.startsWith("rtsp://")).toBe(true);
  });

  it("calls onSubmit with the filled values when Save is clicked", async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const onClose = vi.fn();
    render(<CameraDialog mode="add" onSubmit={onSubmit} onClose={onClose} />);

    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/label/i), "Drive-thru");
    await user.type(
      screen.getByLabelText(/rtsp url/i),
      "rtsp://user:pass@10.0.0.42/stream",
    );
    await user.click(screen.getByLabelText(/lpr/i));
    await user.click(screen.getByRole("button", { name: /save/i }));

    expect(onSubmit).toHaveBeenCalledWith({
      label: "Drive-thru",
      rtspUrl: "rtsp://user:pass@10.0.0.42/stream",
      isLpr: true,
    });
  });

  it("calls onClose when Cancel is clicked", async () => {
    const onClose = vi.fn();
    render(<CameraDialog mode="add" onSubmit={vi.fn()} onClose={onClose} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /cancel/i }));
    expect(onClose).toHaveBeenCalled();
  });

  it("disables Save while the submission is in flight", async () => {
    let resolveSubmit!: () => void;
    const submitPromise = new Promise<void>((resolve) => {
      resolveSubmit = resolve;
    });
    const onSubmit = vi.fn().mockReturnValue(submitPromise);
    render(<CameraDialog mode="add" onSubmit={onSubmit} onClose={vi.fn()} />);

    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/label/i), "x");
    await user.type(screen.getByLabelText(/rtsp url/i), "rtsp://x");
    const save = screen.getByRole("button", { name: /save/i });
    await user.click(save);

    expect(save).toBeDisabled();
    resolveSubmit();
  });

  it("surfaces an error message and re-enables Save when onSubmit rejects", async () => {
    const onSubmit = vi
      .fn()
      .mockRejectedValueOnce(new Error("rtsp_url must begin with rtsp://"));
    render(<CameraDialog mode="add" onSubmit={onSubmit} onClose={vi.fn()} />);

    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/label/i), "x");
    await user.type(screen.getByLabelText(/rtsp url/i), "http://wrong");
    await user.click(screen.getByRole("button", { name: /save/i }));

    expect(await screen.findByText(/rtsp_url must begin with rtsp/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /save/i })).not.toBeDisabled();
  });
});
