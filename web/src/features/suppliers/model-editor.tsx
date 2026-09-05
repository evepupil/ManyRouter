import { Plus, Trash2 } from "lucide-react";
import type { SupplierModel } from "../../api/types";
import {
  Button,
  Checkbox,
  Field,
  IconButton,
  Input,
  Select,
} from "../../components/ui";

export function emptyModel(): SupplierModel {
  return {
    model: "",
    upstream_model: "",
    input_price: "",
    output_price: "",
    currency: "USD",
    enabled: true,
  };
}

export function ModelEditor({
  models,
  onChange,
}: {
  models: SupplierModel[];
  onChange: (models: SupplierModel[]) => void;
}) {
  const update = (index: number, patch: Partial<SupplierModel>) =>
    onChange(
      models.map((model, current) =>
        current === index ? { ...model, ...patch } : model,
      ),
    );
  return (
    <div>
      <div className="section-title">
        <h2>模型采购价</h2>
        <Button
          icon={<Plus />}
          onClick={() => onChange([...models, emptyModel()])}
        >
          添加模型
        </Button>
      </div>
      <div className="model-list">
        {models.map((model, index) => (
          <div key={index} className="form-stack">
            <div className="model-row">
              <Field label="模型名称">
                <Input
                  required
                  maxLength={191}
                  value={model.model}
                  onChange={(event) =>
                    update(index, { model: event.target.value })
                  }
                />
              </Field>
              <Field label="上游模型">
                <Input
                  required
                  maxLength={191}
                  value={model.upstream_model}
                  onChange={(event) =>
                    update(index, { upstream_model: event.target.value })
                  }
                />
              </Field>
              <Field label="输入 / 百万 Token">
                <Input
                  required
                  inputMode="decimal"
                  pattern="[0-9]+(\.[0-9]+)?"
                  value={model.input_price}
                  onChange={(event) =>
                    update(index, { input_price: event.target.value })
                  }
                />
              </Field>
              <Field label="输出 / 百万 Token">
                <Input
                  required
                  inputMode="decimal"
                  pattern="[0-9]+(\.[0-9]+)?"
                  value={model.output_price}
                  onChange={(event) =>
                    update(index, { output_price: event.target.value })
                  }
                />
              </Field>
              <IconButton
                label={`移除模型 ${model.model || index + 1}`}
                disabled={models.length <= 1}
                onClick={() =>
                  onChange(models.filter((_, current) => current !== index))
                }
              >
                <Trash2 size={16} />
              </IconButton>
            </div>
            <div className="toolbar-group">
              <Checkbox
                label="启用模型"
                checked={model.enabled}
                onChange={(event) =>
                  update(index, { enabled: event.target.checked })
                }
              />
              <Select
                className="currency-select"
                aria-label={`模型 ${model.model || index + 1} 的采购币种`}
                value={model.currency}
                onChange={(event) =>
                  update(index, {
                    currency: event.target.value === "CNY" ? "CNY" : "USD",
                  })
                }
              >
                <option value="USD">USD</option>
                <option value="CNY">CNY</option>
              </Select>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
